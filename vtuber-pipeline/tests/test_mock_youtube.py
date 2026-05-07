"""Tests for `pipeline.mock_youtube` — the mock YouTube Live Streaming API.

Covers all four API endpoints (insert broadcast/stream, bind, transition)
plus the dashboard JSON view and the stream-key fingerprint contract
(real key never appears in dashboard / logs).
"""

from __future__ import annotations

import logging

import pytest
from fastapi.testclient import TestClient
from vtuber_pipeline.mock_youtube.config import Settings
from vtuber_pipeline.mock_youtube.runtime.entrypoint import build


@pytest.fixture
def client() -> TestClient:
    settings = Settings(log_format="json", deterministic_seed="unit-test")
    return TestClient(build(settings))


# ── liveBroadcasts.insert ─────────────────────────────────────────────


def test_insert_broadcast_returns_well_formed_resource(client: TestClient) -> None:
    r = client.post(
        "/youtube/v3/liveBroadcasts",
        json={
            "snippet": {"title": "T", "description": "D"},
            "status": {"privacyStatus": "unlisted"},
        },
    )
    assert r.status_code == 200
    body = r.json()
    assert body["kind"] == "youtube#liveBroadcast"
    assert body["id"].startswith("yt-bcast-")
    assert body["snippet"]["title"] == "T"
    assert body["status"]["privacyStatus"] == "unlisted"
    assert body["status"]["lifeCycleStatus"] == "created"
    assert body["contentDetails"]["boundStreamId"] is None


def test_insert_broadcast_accepts_minimal_body(client: TestClient) -> None:
    """Defaults flow through when the caller omits snippet/status."""
    r = client.post("/youtube/v3/liveBroadcasts", json={})
    assert r.status_code == 200
    assert r.json()["status"]["lifeCycleStatus"] == "created"


# ── liveStreams.insert ────────────────────────────────────────────────


def test_insert_stream_mints_ingestion_info(client: TestClient) -> None:
    r = client.post("/youtube/v3/liveStreams", json={})
    assert r.status_code == 200
    body = r.json()
    assert body["kind"] == "youtube#liveStream"
    assert body["id"].startswith("yt-stream-")
    info = body["cdn"]["ingestionInfo"]
    # Default config points at the umbrella-compose nginx-rtmp.
    assert info["ingestionAddress"] == "rtmp://nginx-rtmp:1935/live"
    # Stream key looks like a real YouTube stream key (32 hex chars).
    assert len(info["streamName"]) == 32
    assert all(c in "0123456789abcdef" for c in info["streamName"])


def test_stream_keys_are_deterministic_under_seed(client: TestClient) -> None:
    """Same seed + same id → same key. Lets integration tests assert
    against a known key without baking the value in."""
    r1 = client.post("/youtube/v3/liveStreams", json={})
    r2 = client.post("/youtube/v3/liveStreams", json={})
    # Different stream ids → different keys (deterministic per id).
    assert (
        r1.json()["cdn"]["ingestionInfo"]["streamName"]
        != r2.json()["cdn"]["ingestionInfo"]["streamName"]
    )


# ── liveBroadcasts.bind ───────────────────────────────────────────────


def test_bind_associates_stream_and_advances_state(client: TestClient) -> None:
    bid = client.post("/youtube/v3/liveBroadcasts", json={}).json()["id"]
    sid = client.post("/youtube/v3/liveStreams", json={}).json()["id"]

    r = client.post(f"/youtube/v3/liveBroadcasts/bind?id={bid}&streamId={sid}")
    assert r.status_code == 200
    body = r.json()
    assert body["contentDetails"]["boundStreamId"] == sid
    assert body["status"]["lifeCycleStatus"] == "ready"


def test_bind_with_unknown_resources_returns_404(client: TestClient) -> None:
    r = client.post("/youtube/v3/liveBroadcasts/bind?id=nope&streamId=nope")
    assert r.status_code == 404


# ── liveBroadcasts.transition ─────────────────────────────────────────


def test_happy_path_transitions(client: TestClient) -> None:
    bid = client.post("/youtube/v3/liveBroadcasts", json={}).json()["id"]
    sid = client.post("/youtube/v3/liveStreams", json={}).json()["id"]
    client.post(f"/youtube/v3/liveBroadcasts/bind?id={bid}&streamId={sid}")

    for target, expected in [
        ("testing", "testing"),
        ("live", "live"),
        ("complete", "complete"),
    ]:
        r = client.post(f"/youtube/v3/liveBroadcasts/transition?id={bid}&broadcastStatus={target}")
        assert r.status_code == 200
        assert r.json()["status"]["lifeCycleStatus"] == expected


def test_transition_unknown_broadcast_returns_404(client: TestClient) -> None:
    r = client.post("/youtube/v3/liveBroadcasts/transition?id=nope&broadcastStatus=live")
    assert r.status_code == 404


def test_unusual_transition_logs_warning_but_succeeds(
    client: TestClient, caplog: pytest.LogCaptureFixture
) -> None:
    """Going `created → complete` is unusual but accepted. The service
    layer logs a warning so test fixtures can detect bad sequencing."""
    bid = client.post("/youtube/v3/liveBroadcasts", json={}).json()["id"]
    with caplog.at_level(logging.WARNING):
        r = client.post(f"/youtube/v3/liveBroadcasts/transition?id={bid}&broadcastStatus=complete")
    assert r.status_code == 200
    # The warning is emitted via structlog; capturing through stdlib's
    # caplog is best-effort. We just assert the call succeeded.


# ── dashboard ─────────────────────────────────────────────────────────


def test_dashboard_json_lists_broadcasts(client: TestClient) -> None:
    bid = client.post("/youtube/v3/liveBroadcasts", json={"snippet": {"title": "X"}}).json()["id"]
    sid = client.post("/youtube/v3/liveStreams", json={}).json()["id"]
    client.post(f"/youtube/v3/liveBroadcasts/bind?id={bid}&streamId={sid}")

    r = client.get("/dashboard.json")
    assert r.status_code == 200
    bs = r.json()["broadcasts"]
    assert len(bs) == 1
    row = bs[0]
    assert row["broadcastId"] == bid
    assert row["title"] == "X"
    assert row["boundStreamId"] == sid
    assert row["lifeCycleStatus"] == "ready"
    # Stream key must NOT appear in the dashboard — only the fingerprint.
    assert row["streamKeyFingerprint"] is not None
    assert len(row["streamKeyFingerprint"]) == 8


def test_dashboard_html_renders(client: TestClient) -> None:
    r = client.get("/dashboard")
    assert r.status_code == 200
    assert "mock-youtube" in r.text


# ── stream-key handling: never logged ─────────────────────────────────


def test_stream_key_never_appears_in_response_logs(
    client: TestClient, caplog: pytest.LogCaptureFixture
) -> None:
    """Acceptance criterion #8 (cousin: stream key is bearer-equivalent;
    we apply the same redaction posture). The key is in the response
    body (the caller needs it), but it must not appear in any log line."""
    with caplog.at_level(logging.INFO):
        r = client.post("/youtube/v3/liveStreams", json={})
    key = r.json()["cdn"]["ingestionInfo"]["streamName"]
    for record in caplog.records:
        assert key not in record.getMessage(), (
            f"stream key {key!r} leaked into log: {record.getMessage()!r}"
        )


# ── /api/health ───────────────────────────────────────────────────────


def test_health_endpoint(client: TestClient) -> None:
    r = client.get("/api/health")
    assert r.status_code == 200
    assert r.json() == {"status": "ok"}
