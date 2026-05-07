"""Unit tests for `pipeline.streams.service.StreamLifecycle`.

Covers the create() / stop() orchestration: register egress, open
bridge session, persist record, then revoke/complete on stop.
"""

from __future__ import annotations

from typing import Any

import pytest
from vtuber_pipeline.streams.providers import (
    BridgeSessionOpenResult,
    EgressRegistration,
    YouTubeBroadcast,
)
from vtuber_pipeline.streams.repo import StreamRepository
from vtuber_pipeline.streams.service import (
    StreamAlreadyEndedError,
    StreamLifecycle,
    StreamNotFoundError,
)
from vtuber_pipeline.streams.types import StreamCreateRequest, StreamState

pytestmark = pytest.mark.asyncio


# ── fakes ──────────────────────────────────────────────────────────────


class _FakeBridge:
    def __init__(self) -> None:
        self.calls: list[dict[str, Any]] = []
        self.session_counter = 0
        self.fail = False

    async def open_session(
        self, *, customer_bearer: str, params: dict[str, Any]
    ) -> BridgeSessionOpenResult:
        if self.fail:
            raise RuntimeError("bridge failed")
        self.session_counter += 1
        sid = f"bridge-sess-{self.session_counter}"
        self.calls.append({"customer_bearer": customer_bearer, "params": params})
        return BridgeSessionOpenResult(
            bridge_session_id=sid,
            customer_control_url=f"http://b/v1/vtuber/sessions/{sid}/control",
            session_child_bearer=f"vtbs_{sid}",
            expires_at="2027-01-01T00:00:00Z",
        )

    async def close(self) -> None:
        pass


class _FakeEgress:
    def __init__(self) -> None:
        self.registered: list[str] = []
        self.revoked: list[str] = []

    async def register(
        self, *, session_id: str, rtmp_url: str, stream_key: str
    ) -> EgressRegistration:
        _ = (rtmp_url, stream_key)
        self.registered.append(session_id)
        return EgressRegistration(
            session_id=session_id,
            egress_url=f"http://egress/sessions/{session_id}/media",
            auth=f"Bearer pl_egress_{session_id}_test",
        )

    async def revoke(self, *, session_id: str) -> None:
        self.revoked.append(session_id)

    async def close(self) -> None:
        pass


class _FakeYouTube:
    def __init__(self) -> None:
        self.broadcasts_created = 0
        self.completed: list[str | None] = []

    async def create_broadcast(
        self, *, title: str, description: str, privacy: str
    ) -> YouTubeBroadcast:
        _ = (title, description, privacy)
        self.broadcasts_created += 1
        return YouTubeBroadcast(
            broadcast_id=f"yt-{self.broadcasts_created}",
            rtmp_url="rtmp://test/live",
            stream_key=f"key-{self.broadcasts_created}",
        )

    async def complete_broadcast(self, broadcast_id: str | None) -> None:
        self.completed.append(broadcast_id)

    async def close(self) -> None:
        pass


def _create_request() -> StreamCreateRequest:
    return StreamCreateRequest.model_validate(
        {
            "persona": {"name": "Aria", "system_prompt": "be friendly"},
            "avatar": {"vrm_url": "https://cdn.test/a.vrm"},
            "voice": {"provider": "kokoro", "voice_id": "nova"},
            "llm": {"model": "vtuber-default"},
            "render": {"width": 640, "height": 360, "fps": 30, "bitrate_bps": 1500000},
        }
    )


def _build_lifecycle() -> tuple[
    StreamLifecycle, StreamRepository, _FakeBridge, _FakeEgress, _FakeYouTube
]:
    repo = StreamRepository()
    bridge = _FakeBridge()
    egress = _FakeEgress()
    youtube = _FakeYouTube()
    lifecycle = StreamLifecycle(
        repo=repo,
        bridge=bridge,  # type: ignore[arg-type]
        egress=egress,  # type: ignore[arg-type]
        youtube=youtube,  # type: ignore[arg-type]
        bridge_customer_bearer="sk-test-pipeline",
        bridge_public_url="http://b",
        public_base_url="http://p",
        default_rtmp_url="rtmp://nginx-rtmp:1935/live",
    )
    return lifecycle, repo, bridge, egress, youtube


# ── tests ──────────────────────────────────────────────────────────────


async def test_create_orchestrates_register_then_open_then_persist() -> None:
    lifecycle, repo, bridge, egress, youtube = _build_lifecycle()

    resp = await lifecycle.create(_create_request())

    assert resp.stream_id.startswith("strm-")
    assert resp.state is StreamState.STARTING
    assert resp.events_url == f"http://p/api/streams/{resp.stream_id}/events"
    assert resp.youtube_broadcast_id == "yt-1"

    # Order: youtube create → egress register → bridge open.
    assert youtube.broadcasts_created == 1
    assert egress.registered == [resp.stream_id]
    assert len(bridge.calls) == 1

    # Bridge gets customer's persona/avatar/voice/llm/render, plus
    # the egress URL+auth from the egress register response.
    body = bridge.calls[0]["params"]
    assert body["persona"]["name"] == "Aria"
    assert body["avatar"]["vrm_url"] == "https://cdn.test/a.vrm"
    assert body["egress"]["url"].endswith("/media")
    assert body["egress"]["auth"].startswith("Bearer pl_egress_")

    # StreamRecord persisted.
    rec = await repo.get(resp.stream_id)
    assert rec is not None
    assert rec.bridge_session_id == "bridge-sess-1"
    assert rec.bridge_session_child_bearer == "vtbs_bridge-sess-1"


async def test_get_returns_not_found() -> None:
    lifecycle, *_ = _build_lifecycle()
    with pytest.raises(StreamNotFoundError):
        await lifecycle.get("strm-missing")


async def test_stop_revokes_egress_completes_youtube_marks_ended() -> None:
    lifecycle, _repo, _bridge, egress, youtube = _build_lifecycle()
    resp = await lifecycle.create(_create_request())

    rec = await lifecycle.stop(resp.stream_id)
    assert rec.state is StreamState.ENDED
    assert egress.revoked == [resp.stream_id]
    assert youtube.completed == ["yt-1"]


async def test_stop_twice_raises() -> None:
    lifecycle, *_ = _build_lifecycle()
    resp = await lifecycle.create(_create_request())
    await lifecycle.stop(resp.stream_id)
    with pytest.raises(StreamAlreadyEndedError):
        await lifecycle.stop(resp.stream_id)


async def test_create_propagates_bridge_failure() -> None:
    lifecycle, repo, bridge, *_ = _build_lifecycle()
    bridge.fail = True
    with pytest.raises(RuntimeError):
        await lifecycle.create(_create_request())
    # No record persisted on a failed open.
    assert not await repo.list_all()


async def test_create_request_rejects_extra_fields() -> None:
    """Schema-level pinning: customers can't smuggle bridge fields
    through the streams API."""
    from pydantic import ValidationError

    with pytest.raises(ValidationError):
        StreamCreateRequest.model_validate(
            {
                "persona": {"name": "x", "system_prompt": "hi"},
                "avatar": {"vrm_url": "https://x"},
                "voice": {"provider": "k", "voice_id": "n"},
                "llm": {"model": "m"},
                "render": {"width": 320, "height": 180, "fps": 30, "bitrate_bps": 500_000},
                "egress": {"url": "x", "auth": "y"},  # forbidden
            }
        )
