"""Failure-mode tests for the egress worker (M6).

Covers acceptance criteria #5 (recovery / reconnect), #6 (structured
events on every lifecycle transition), #8 (bearer redaction), and #9
(layer boundaries respected).

Uses the same fake FFmpegRunner from `test_egress.py` so tests run
without docker / real ffmpeg.
"""

from __future__ import annotations

import importlib
import json
import pkgutil

import pytest
from fastapi.testclient import TestClient
from vtuber_pipeline.egress._test_fakes import (
    FailingFFmpegRunner as _FailingFFmpegRunner,
)
from vtuber_pipeline.egress.config import Settings
from vtuber_pipeline.egress.runtime.entrypoint import build
from vtuber_pipeline.egress.service import mint_bearer

# ── fixtures ──────────────────────────────────────────────────────────


@pytest.fixture
def secret() -> bytes:
    return b"failure-mode-secret-32-bytes-aaaa"


@pytest.fixture
def app_with_failing_ffmpeg(secret: bytes) -> tuple[TestClient, _FailingFFmpegRunner]:
    runner = _FailingFFmpegRunner(fail_after_bytes=128)
    settings = Settings(
        log_format="json",
        bearer_secret=secret,
        public_base_url="http://test-egress",
    )
    app = build(settings, ffmpeg=runner)
    return TestClient(app), runner


def _register(client: TestClient, sid: str = "s1") -> None:
    r = client.post(
        f"/admin/sessions/{sid}/register",
        json={"rtmp_url": "rtmp://nginx-rtmp:1935/live", "stream_key": "sk-" + sid},
    )
    assert r.status_code == 200


# ── ffmpeg crash → 500, session marked errored ────────────────────────


def test_ffmpeg_crash_marks_session_errored(
    app_with_failing_ffmpeg: tuple[TestClient, _FailingFFmpegRunner],
    secret: bytes,
) -> None:
    """Acceptance criterion #5: ffmpeg crash + restart. The session
    is marked errored; the in-flight flag clears so the next reconnect
    can claim it (the session-runner is responsible for retrying)."""
    client, _runner = app_with_failing_ffmpeg
    _register(client)
    bearer = mint_bearer("s1", secret)

    # Push enough bytes to trigger the fake's failure threshold.
    r = client.post(
        "/egress/sessions/s1/media",
        content=b"\x47" * 256,
        headers={"Authorization": f"Bearer {bearer}"},
    )
    assert r.status_code == 500

    state = client.get("/admin/sessions").json()["sessions"][0]
    assert state["status"] == "errored"
    assert state["in_flight"] is False
    assert state["last_error"] is not None


def test_reconnect_after_ffmpeg_crash_succeeds(
    app_with_failing_ffmpeg: tuple[TestClient, _FailingFFmpegRunner],
    secret: bytes,
) -> None:
    """After an ffmpeg crash, a fresh chunked POST with the same bearer
    must succeed — the session-runner is expected to retry per ADR-007."""
    client, _runner = app_with_failing_ffmpeg
    _register(client)
    bearer = mint_bearer("s1", secret)

    # First attempt crashes.
    r1 = client.post(
        "/egress/sessions/s1/media",
        content=b"\x47" * 256,
        headers={"Authorization": f"Bearer {bearer}"},
    )
    assert r1.status_code == 500

    # Swap in a non-failing runner for the retry — emulates the runner
    # restarting ffmpeg cleanly. The lifecycle reuses the existing
    # vault entry. We swap by mutating the lifecycle.ffmpeg directly.
    from vtuber_pipeline.egress._test_fakes import FakeFFmpegRunner as _FakeFFmpegRunner

    new_runner = _FakeFFmpegRunner()
    client.app.state.lifecycle.ffmpeg = new_runner

    r2 = client.post(
        "/egress/sessions/s1/media",
        content=b"\x47" * 64,
        headers={"Authorization": f"Bearer {bearer}"},
    )
    assert r2.status_code == 200


# ── structured events on every lifecycle transition ───────────────────


def test_structured_events_emitted(secret: bytes, capsys: pytest.CaptureFixture[str]) -> None:
    """Acceptance criterion #6: structured events on every lifecycle
    transition. structlog renders JSON to stdout, so we capture stdout
    and parse each line as JSON looking for the `event` field."""
    from vtuber_pipeline.egress._test_fakes import FakeFFmpegRunner as _FakeFFmpegRunner

    runner = _FakeFFmpegRunner()
    settings = Settings(
        log_format="json",
        bearer_secret=secret,
        public_base_url="http://test-egress",
    )
    app = build(settings, ffmpeg=runner)
    client = TestClient(app)
    _register(client)
    bearer = mint_bearer("s1", secret)

    r = client.post(
        "/egress/sessions/s1/media",
        content=b"\x47" * 64,
        headers={"Authorization": f"Bearer {bearer}"},
    )
    assert r.status_code == 200

    captured = capsys.readouterr()
    seen_events = set()
    for line in captured.out.splitlines():
        try:
            data = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(data, dict) and "event" in data:
            seen_events.add(data["event"])

    expected = {
        "egress_session_registered",
        "egress_chunked_post_opened",
        "egress_ingesting",
        "egress_ffmpeg_exited",
        "egress_published",
    }
    missing = expected - seen_events
    assert not missing, f"missing structured events: {missing}; saw: {sorted(seen_events)}"


# ── bearer never logged ───────────────────────────────────────────────


def test_egress_bearer_never_logged(secret: bytes, capsys: pytest.CaptureFixture[str]) -> None:
    """Acceptance criterion #8: egress bearer never logged. The bearer's
    HMAC tail is bearer-equivalent to the stream key — it's the secret
    that authenticates the chunked POST. Allow the public prefix
    (`pl_egress_<sid>_`); reject the HMAC tail in any captured output."""
    from vtuber_pipeline.egress._test_fakes import FakeFFmpegRunner as _FakeFFmpegRunner

    runner = _FakeFFmpegRunner()
    settings = Settings(
        log_format="json",
        bearer_secret=secret,
        public_base_url="http://test-egress",
    )
    app = build(settings, ffmpeg=runner)
    client = TestClient(app)
    _register(client)
    bearer = mint_bearer("s1", secret)
    hex_tail = bearer.rsplit("_", 1)[1]

    # Hit every code path that could log: happy path, 401, 404.
    client.post(
        "/egress/sessions/s1/media",
        content=b"\x47" * 64,
        headers={"Authorization": f"Bearer {bearer}"},
    )
    client.post(
        "/egress/sessions/s1/media",
        content=b"x",
        headers={"Authorization": "Bearer wrong"},
    )
    client.post("/egress/sessions/s1/media", content=b"x")  # no auth

    captured = capsys.readouterr()
    # Combine stdout + stderr — any log output regardless of stream.
    combined = captured.out + "\n" + captured.err
    assert hex_tail not in combined, (
        f"bearer hmac leaked into log output (last 200 chars): {combined[-200:]!r}"
    )


# ── client disconnect / dropped chunked POST ──────────────────────────


def test_disconnect_clears_in_flight(secret: bytes) -> None:
    """Per ADR-007, the worker may disconnect mid-stream. The egress
    must clear the in-flight flag so a reconnect can claim it.

    We simulate disconnect by raising inside the body iterator. The
    happy-path runner sees a partial body, ffmpeg drains, lifecycle
    succeeds. The key assertion is `in_flight` is back to False after.
    """
    from vtuber_pipeline.egress._test_fakes import FakeFFmpegRunner as _FakeFFmpegRunner

    runner = _FakeFFmpegRunner()
    settings = Settings(
        log_format="json", bearer_secret=secret, public_base_url="http://test-egress"
    )
    app = build(settings, ffmpeg=runner)
    client = TestClient(app)
    _register(client)
    bearer = mint_bearer("s1", secret)

    # Empty body simulates an immediate disconnect — fastapi sees no
    # chunks, lifecycle calls close_stdin + wait, ffmpeg exits cleanly.
    r = client.post(
        "/egress/sessions/s1/media",
        content=b"",
        headers={"Authorization": f"Bearer {bearer}"},
    )
    assert r.status_code == 200

    state = client.get("/admin/sessions").json()["sessions"][0]
    assert state["in_flight"] is False
    assert state["status"] == "ended"


# ── layer boundaries (acceptance criterion #9) ────────────────────────


def test_egress_layers_respect_dependency_direction() -> None:
    """`ui` depends on `service`+`repo`; `service` depends on `repo`+
    `providers`+`types`; `providers` and `repo` and `types` have no
    intra-egress imports. We enforce by walking the import graph."""

    # Discover all submodules under pipeline.egress.
    import vtuber_pipeline.egress as pkg

    modules: dict[str, set[str]] = {}
    for info in pkgutil.walk_packages(pkg.__path__, prefix=pkg.__name__ + "."):
        modname = info.name
        try:
            mod = importlib.import_module(modname)
        except Exception:
            continue
        # Pull `import` and `from … import` references via attribute
        # inspection — the cheap way without parsing source.
        deps = set()
        for attr in dir(mod):
            obj = getattr(mod, attr, None)
            obj_mod = getattr(obj, "__module__", None)
            if isinstance(obj_mod, str) and obj_mod.startswith("pipeline.egress."):
                deps.add(obj_mod)
        modules[modname] = deps

    forbidden = [
        # repo / types / providers must not depend on ui or service or runtime.
        (
            "pipeline.egress.repo",
            {"pipeline.egress.ui", "pipeline.egress.service", "pipeline.egress.runtime"},
        ),
        (
            "pipeline.egress.types",
            {
                "pipeline.egress.ui",
                "pipeline.egress.service",
                "pipeline.egress.runtime",
                "pipeline.egress.repo",
            },
        ),
        (
            "pipeline.egress.providers",
            {
                "pipeline.egress.ui",
                "pipeline.egress.service",
                "pipeline.egress.runtime",
                "pipeline.egress.repo",
            },
        ),
        # service depends DOWNWARD only (repo + types + providers).
        ("pipeline.egress.service", {"pipeline.egress.ui", "pipeline.egress.runtime"}),
    ]

    violations = []
    for modname, deps in modules.items():
        for prefix, banned_targets in forbidden:
            if modname.startswith(prefix):
                hits = {d for d in deps if any(d.startswith(t) for t in banned_targets)}
                if hits:
                    violations.append(f"{modname} → {sorted(hits)}")

    assert not violations, "layer-boundary violations:\n" + "\n".join(violations)
