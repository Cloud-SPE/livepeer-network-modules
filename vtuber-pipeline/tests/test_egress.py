"""Tests for `pipeline.egress` — the chunked-POST → ffmpeg worker.

Covers M2 acceptance criteria from
docs/exec-plans/active/mock-youtube-egress.md:

  - HMAC bearer validation against Pipeline-issued egress bearer.
  - Single-use POST semantics (409 on concurrent POST).
  - 401 on missing/invalid bearer; 404 on unregistered session.
  - Stream key never appears in logs (acceptance #7-#8 cousin).

The ffmpeg subprocess is replaced by a fake runner so these tests run
without needing a real ffmpeg or RTMP sink. Real-ffmpeg integration is
exercised in M5.
"""

from __future__ import annotations

import logging

import pytest
from fastapi.testclient import TestClient
from vtuber_pipeline.egress._test_fakes import FakeFFmpegRunner
from vtuber_pipeline.egress.config import Settings
from vtuber_pipeline.egress.runtime.entrypoint import build
from vtuber_pipeline.egress.service import (
    mint_bearer,
    parse_bearer,
    validate_bearer,
)

# ── fixtures ──────────────────────────────────────────────────────────


@pytest.fixture
def secret() -> bytes:
    return b"unit-test-secret-32-bytes-padding!"


@pytest.fixture
def runner() -> FakeFFmpegRunner:
    return FakeFFmpegRunner()


@pytest.fixture
def app_client(secret: bytes, runner: FakeFFmpegRunner) -> TestClient:
    settings = Settings(
        log_format="json",
        bearer_secret=secret,
        public_base_url="http://test-egress",
    )
    app = build(settings, ffmpeg=runner)
    return TestClient(app)


def _register_session(client: TestClient, session_id: str) -> dict:
    r = client.post(
        f"/admin/sessions/{session_id}/register",
        json={"rtmp_url": "rtmp://nginx-rtmp:1935/live", "stream_key": "secret-stream-key-xyz"},
    )
    assert r.status_code == 200, r.text
    return r.json()


# ── bearer parse + validate ───────────────────────────────────────────


def test_mint_and_parse_round_trip(secret: bytes) -> None:
    bearer_str = mint_bearer("session-abc", secret)
    parsed = parse_bearer(f"Bearer {bearer_str}")
    assert parsed is not None
    assert parsed.session_id == "session-abc"
    assert validate_bearer(parsed, expected_session_id="session-abc", secret=secret)


def test_validate_rejects_wrong_session_id(secret: bytes) -> None:
    bearer_str = mint_bearer("session-abc", secret)
    parsed = parse_bearer(f"Bearer {bearer_str}")
    assert parsed is not None
    assert not validate_bearer(parsed, expected_session_id="session-xyz", secret=secret)


def test_validate_rejects_wrong_secret(secret: bytes) -> None:
    bearer_str = mint_bearer("session-abc", secret)
    parsed = parse_bearer(f"Bearer {bearer_str}")
    assert parsed is not None
    assert not validate_bearer(parsed, expected_session_id="session-abc", secret=b"other-secret")


def test_parse_rejects_malformed_input() -> None:
    for bad in ["", "Bearer ", "Bearer pl_egress_", "Token foo", "pl_egress_x_y"]:
        assert parse_bearer(bad) is None, f"expected parse to reject {bad!r}"


# ── /admin/sessions ───────────────────────────────────────────────────


def test_register_returns_egress_url_and_bearer(app_client: TestClient) -> None:
    body = _register_session(app_client, "session-abc")
    assert body["session_id"] == "session-abc"
    assert body["egress_url"] == "http://test-egress/egress/sessions/session-abc/media"
    assert body["auth"].startswith("Bearer pl_egress_session-abc_")


def test_revoke_drops_registration(app_client: TestClient) -> None:
    _register_session(app_client, "session-abc")
    r = app_client.delete("/admin/sessions/session-abc")
    assert r.status_code == 200
    sessions = app_client.get("/admin/sessions").json()["sessions"]
    assert sessions == []


def test_list_sessions(app_client: TestClient) -> None:
    _register_session(app_client, "s1")
    _register_session(app_client, "s2")
    sessions = app_client.get("/admin/sessions").json()["sessions"]
    ids = sorted(s["session_id"] for s in sessions)
    assert ids == ["s1", "s2"]


# ── /egress/sessions/{sid}/media — auth surface ───────────────────────


def test_media_post_without_bearer_returns_401(app_client: TestClient) -> None:
    _register_session(app_client, "s1")
    r = app_client.post("/egress/sessions/s1/media", content=b"x")
    assert r.status_code == 401


def test_media_post_with_wrong_bearer_returns_401(app_client: TestClient) -> None:
    _register_session(app_client, "s1")
    r = app_client.post(
        "/egress/sessions/s1/media",
        content=b"x",
        headers={"Authorization": "Bearer pl_egress_s1_deadbeef"},
    )
    assert r.status_code == 401


def test_media_post_for_unregistered_session_returns_404(
    app_client: TestClient, secret: bytes
) -> None:
    bearer = mint_bearer("ghost", secret)
    r = app_client.post(
        "/egress/sessions/ghost/media",
        content=b"x",
        headers={"Authorization": f"Bearer {bearer}"},
    )
    assert r.status_code == 404


# ── happy path ────────────────────────────────────────────────────────


def test_happy_path_drives_ffmpeg(
    app_client: TestClient, secret: bytes, runner: FakeFFmpegRunner
) -> None:
    body = _register_session(app_client, "s1")
    bearer = body["auth"].removeprefix("Bearer ")
    payload = b"\x47" * 4096  # MP2T sync byte * 4KB (shape only; not real frames)
    r = app_client.post(
        "/egress/sessions/s1/media",
        content=payload,
        headers={"Authorization": f"Bearer {bearer}"},
    )
    assert r.status_code == 200
    assert r.json() == {"status": "ok", "session_id": "s1"}
    assert len(runner.processes) == 1
    proc = runner.processes[0]
    assert proc.bytes_in == 4096
    assert proc.rtmp_target == "rtmp://nginx-rtmp:1935/live/secret-stream-key-xyz"
    assert proc.terminated is False  # graceful close, not termination


# ── single-use POST semantics ─────────────────────────────────────────


def test_concurrent_post_returns_409(app_client: TestClient, secret: bytes) -> None:
    """Once a chunked POST is open for a session-id, subsequent POSTs
    with the same bearer reject (409). Acceptance criterion #2.

    We simulate "in-flight" by directly flipping the vault's in-flight
    flag — same effect as a request mid-flight, but without the
    threading dance that fights TestClient's serialization. The
    end-to-end concurrent-stream behavior is exercised in M5 with real
    HTTP clients.
    """
    _register_session(app_client, "s1")
    bearer = mint_bearer("s1", secret)

    # Manually claim the in-flight slot the way a live request would.
    vault = app_client.app.state.vault
    assert vault.mark_in_flight("s1") is True

    r = app_client.post(
        "/egress/sessions/s1/media",
        content=b"\x47" * 64,
        headers={"Authorization": f"Bearer {bearer}"},
    )
    assert r.status_code == 409
    # The 409 path must not have spawned an ffmpeg.
    assert app_client.app.state.lifecycle.ffmpeg.processes == []  # type: ignore[attr-defined]


def test_reconnect_after_disconnect_is_allowed(app_client: TestClient, secret: bytes) -> None:
    """Per ADR-007: workers may reconnect after a dropped chunked POST.
    Once the first request completes (in-flight flag clears), a new
    POST should succeed."""
    _register_session(app_client, "s1")
    bearer = mint_bearer("s1", secret)
    h = {"Authorization": f"Bearer {bearer}"}
    r1 = app_client.post("/egress/sessions/s1/media", content=b"x", headers=h)
    assert r1.status_code == 200
    r2 = app_client.post("/egress/sessions/s1/media", content=b"x", headers=h)
    assert r2.status_code == 200


# ── stream key never logged ───────────────────────────────────────────


def test_stream_key_never_logged(
    app_client: TestClient, secret: bytes, caplog: pytest.LogCaptureFixture
) -> None:
    """Acceptance criteria #7+#8 cousin: the stream key (bearer-equivalent)
    must not appear in any log line. We register a session with a
    distinctive key and assert it never shows up in captured logs."""
    distinctive_key = "STREAM_KEY_NEEDLE_" + "X" * 32
    app_client.post(
        "/admin/sessions/s1/register",
        json={"rtmp_url": "rtmp://nginx-rtmp:1935/live", "stream_key": distinctive_key},
    )
    bearer = mint_bearer("s1", secret)

    with caplog.at_level(logging.INFO):
        app_client.post(
            "/egress/sessions/s1/media",
            content=b"\x47" * 64,
            headers={"Authorization": f"Bearer {bearer}"},
        )

    for record in caplog.records:
        assert distinctive_key not in record.getMessage(), (
            f"stream key leaked: {record.getMessage()!r}"
        )


# ── stream key never persisted (no disk writes) ───────────────────────


def test_stream_key_never_written_to_disk(
    app_client: TestClient, secret: bytes, tmp_path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Acceptance criterion #7: stream key never persisted. We can't
    cheaply intercept every possible write, but we can scan the temp
    dir + cwd for the key after a session and assert it's not there."""
    distinctive_key = "STREAM_KEY_NEEDLE_DISK_" + "Y" * 32
    monkeypatch.chdir(tmp_path)

    app_client.post(
        "/admin/sessions/s1/register",
        json={"rtmp_url": "rtmp://nginx-rtmp:1935/live", "stream_key": distinctive_key},
    )
    bearer = mint_bearer("s1", secret)
    app_client.post(
        "/egress/sessions/s1/media",
        content=b"\x47" * 64,
        headers={"Authorization": f"Bearer {bearer}"},
    )

    # Walk the tmp_path looking for the key.
    for path in tmp_path.rglob("*"):
        if path.is_file():
            try:
                content = path.read_bytes()
                assert distinctive_key.encode() not in content, (
                    f"stream key found on disk at {path}"
                )
            except (OSError, UnicodeDecodeError):
                pass


# ── /api/health ───────────────────────────────────────────────────────


def test_health_endpoint(app_client: TestClient) -> None:
    r = app_client.get("/api/health")
    assert r.status_code == 200
    assert r.json() == {"status": "ok"}
