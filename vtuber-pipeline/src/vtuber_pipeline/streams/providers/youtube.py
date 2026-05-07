"""YouTube binder: creates a live broadcast and returns the
{rtmp_url, stream_key} pair the egress should publish into.

`MockYouTubeBinder` talks to `pipeline.mock_youtube` (dev). A
`RealYouTubeBinder` (Google OAuth + real broadcast lifecycle) is a
separate plan.

`NoneYouTubeBinder` short-circuits to (default_rtmp_url, generated stream_key)
so the dev demo can run against nginx-rtmp directly without a YouTube
binder service.
"""

from __future__ import annotations

import secrets
from dataclasses import dataclass
from typing import Protocol

import httpx


@dataclass(frozen=True)
class YouTubeBroadcast:
    broadcast_id: str | None
    rtmp_url: str
    stream_key: str


class YouTubeBinder(Protocol):
    async def create_broadcast(
        self, *, title: str, description: str, privacy: str
    ) -> YouTubeBroadcast: ...

    async def complete_broadcast(self, broadcast_id: str | None) -> None: ...

    async def close(self) -> None: ...


class NoneYouTubeBinder:
    """No-op binder for the dev demo path. Returns the operator's
    `default_rtmp_url` + a fresh stream_key. No real broadcast created."""

    def __init__(self, *, default_rtmp_url: str) -> None:
        self._default_rtmp_url = default_rtmp_url

    async def create_broadcast(
        self, *, title: str, description: str, privacy: str
    ) -> YouTubeBroadcast:
        _ = (title, description, privacy)
        return YouTubeBroadcast(
            broadcast_id=None,
            rtmp_url=self._default_rtmp_url,
            stream_key=f"stream-{secrets.token_urlsafe(8)}",
        )

    async def complete_broadcast(self, broadcast_id: str | None) -> None:
        _ = broadcast_id

    async def close(self) -> None:
        pass


class MockYouTubeBinder:
    """Talks to `pipeline.mock_youtube`'s YouTube Live Streaming API
    stand-in. Mirrors the prod flow: createBroadcast → createStream →
    bind. mock-youtube returns a deterministic (rtmp_url, stream_key)
    pair we can publish into."""

    def __init__(
        self,
        *,
        base_url: str,
        timeout_secs: float = 10.0,
        client: httpx.AsyncClient | None = None,
    ) -> None:
        self._base_url = base_url.rstrip("/")
        self._timeout_secs = timeout_secs
        self._client = client
        self._owns_client = client is None

    def _ensure_client(self) -> httpx.AsyncClient:
        if self._client is None:
            self._client = httpx.AsyncClient(timeout=self._timeout_secs)
        return self._client

    async def create_broadcast(
        self, *, title: str, description: str, privacy: str
    ) -> YouTubeBroadcast:
        # mock-youtube's exact route surface mirrors a subset of the
        # YouTube Live Streaming API. The shape captured here is
        # documented in pipeline.mock_youtube's README; we do a single
        # combined create-and-bind call via /youtube/v3/liveBroadcasts.
        c = self._ensure_client()
        broadcast_resp = await c.post(
            f"{self._base_url}/youtube/v3/liveBroadcasts",
            json={
                "snippet": {"title": title, "description": description},
                "status": {"privacyStatus": privacy},
            },
        )
        broadcast_resp.raise_for_status()
        broadcast = broadcast_resp.json()
        broadcast_id = broadcast["id"]

        stream_resp = await c.post(
            f"{self._base_url}/youtube/v3/liveStreams",
            json={"snippet": {"title": f"{title} (stream)"}},
        )
        stream_resp.raise_for_status()
        stream = stream_resp.json()
        rtmp_url = stream["cdn"]["ingestionInfo"]["ingestionAddress"]
        stream_key = stream["cdn"]["ingestionInfo"]["streamName"]

        # bind broadcast → stream
        await c.post(
            f"{self._base_url}/youtube/v3/liveBroadcasts/{broadcast_id}/bind",
            json={"streamId": stream["id"]},
        )

        return YouTubeBroadcast(
            broadcast_id=broadcast_id,
            rtmp_url=rtmp_url,
            stream_key=stream_key,
        )

    async def complete_broadcast(self, broadcast_id: str | None) -> None:
        if not broadcast_id:
            return
        await self._ensure_client().post(
            f"{self._base_url}/youtube/v3/liveBroadcasts/{broadcast_id}/transition",
            params={"broadcastStatus": "complete"},
        )

    async def close(self) -> None:
        if self._owns_client and self._client is not None:
            await self._client.aclose()
            self._client = None
