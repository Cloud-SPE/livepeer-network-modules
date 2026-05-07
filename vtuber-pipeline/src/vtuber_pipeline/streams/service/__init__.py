"""StreamLifecycle — the orchestration brain of pipeline.streams.

Sequence on create():

  1. Allocate stream_id + a fresh stream_key.
  2. Optionally call YouTubeBinder.create_broadcast → returns
     (rtmp_url, stream_key). When skipped (NoneYouTubeBinder), use
     the operator's default_rtmp_url and the generated stream_key.
  3. Call EgressAdminClient.register(session_id=stream_id,
     rtmp_url, stream_key) → returns the egress_url + signed bearer.
  4. Call GatewayClient.open_session with persona/avatar/voice/llm/
     render + the egress block.
  5. Persist StreamRecord to the repo.
  6. Return StreamCreateResponse.

stop() reverses: gateway close → egress revoke → youtube complete →
mark ended.
"""

from __future__ import annotations

import contextlib
import secrets
from datetime import UTC, datetime, timedelta
from typing import Any

from vtuber_pipeline.streams.providers import (
    EgressAdminClient,
    GatewayClient,
    YouTubeBinder,
)
from vtuber_pipeline.streams.repo import StreamRepository
from vtuber_pipeline.streams.types import (
    StreamCreateRequest,
    StreamCreateResponse,
    StreamRecord,
    StreamState,
)


class StreamNotFoundError(KeyError):
    pass


class StreamAlreadyEndedError(RuntimeError):
    pass


class StreamLifecycle:
    def __init__(
        self,
        *,
        repo: StreamRepository,
        gateway: GatewayClient,
        egress: EgressAdminClient,
        youtube: YouTubeBinder,
        gateway_customer_bearer: str,
        gateway_public_url: str,
        public_base_url: str,
        default_rtmp_url: str,
        hls_preview_template: str | None = None,
        session_ttl: timedelta = timedelta(hours=3),
    ) -> None:
        if not gateway_customer_bearer:
            raise ValueError("gateway_customer_bearer required")
        self._repo = repo
        self._gateway = gateway
        self._egress = egress
        self._youtube = youtube
        self._gateway_customer_bearer = gateway_customer_bearer
        self._gateway_public_url = gateway_public_url.rstrip("/")
        self._public_base_url = public_base_url.rstrip("/")
        self._default_rtmp_url = default_rtmp_url
        self._hls_preview_template = hls_preview_template
        self._session_ttl = session_ttl

    async def create(self, req: StreamCreateRequest) -> StreamCreateResponse:
        stream_id = f"strm-{secrets.token_urlsafe(12)}"

        # 1. YouTube binder (or NoneYouTubeBinder for the dev path).
        if req.youtube is not None:
            yt = await self._youtube.create_broadcast(
                title=req.youtube.title,
                description=req.youtube.description,
                privacy=req.youtube.privacy,
            )
        else:
            yt = await self._youtube.create_broadcast(
                title="dev stream", description="", privacy="private"
            )

        # 2. Register egress with the rtmp destination.
        registration = await self._egress.register(
            session_id=stream_id,
            rtmp_url=yt.rtmp_url,
            stream_key=yt.stream_key,
        )

        # 3. Open the vtuber session via the gateway.
        params = self._build_gateway_params(
            req=req,
            egress_url=registration.egress_url,
            egress_auth=registration.auth,
        )
        gateway_result = await self._gateway.open_session(
            customer_bearer=self._gateway_customer_bearer,
            params=params,
        )

        # 4. Persist + return.
        now = datetime.now(UTC)
        record = StreamRecord(
            stream_id=stream_id,
            state=StreamState.STARTING,
            started_at=now,
            gateway_session_id=gateway_result.gateway_session_id,
            gateway_session_child_bearer=gateway_result.session_child_bearer,
            egress_session_id=stream_id,
            rtmp_url=yt.rtmp_url,
            stream_key=yt.stream_key,
            create_request=req.model_dump(),
            youtube_broadcast_id=yt.broadcast_id,
        )
        await self._repo.insert(record)

        events_url = f"{self._public_base_url}/api/streams/{stream_id}/events"
        hls_preview_url: str | None = None
        if self._hls_preview_template:
            hls_preview_url = self._hls_preview_template.replace("{stream_key}", yt.stream_key)

        return StreamCreateResponse(
            stream_id=stream_id,
            state=StreamState.STARTING,
            events_url=events_url,
            hls_preview_url=hls_preview_url,
            expires_at=now + self._session_ttl,
            youtube_broadcast_id=yt.broadcast_id,
        )

    async def get(self, stream_id: str) -> StreamRecord:
        rec = await self._repo.get(stream_id)
        if rec is None:
            raise StreamNotFoundError(stream_id)
        return rec

    async def stop(self, stream_id: str) -> StreamRecord:
        rec = await self._repo.get(stream_id)
        if rec is None:
            raise StreamNotFoundError(stream_id)
        if rec.state in (StreamState.ENDED, StreamState.ERRORED):
            raise StreamAlreadyEndedError(rec.state.value)

        rec.state = StreamState.STOPPING
        await self._repo.update(rec)

        # 1. Revoke the egress session (best-effort; the gateway's session
        # close above will tear down the chunked-POST anyway).
        with contextlib.suppress(Exception):
            await self._egress.revoke(session_id=rec.egress_session_id)

        # 2. Complete the YouTube broadcast (best-effort).
        with contextlib.suppress(Exception):
            await self._youtube.complete_broadcast(rec.youtube_broadcast_id)

        # 3. Mark ended. (The gateway doesn't have a session-close
        # endpoint yet; the session will tear down when the WS closes
        # or the worker times out — separate plan.)
        rec.state = StreamState.ENDED
        await self._repo.update(rec)
        return rec

    def _build_gateway_params(
        self,
        *,
        req: StreamCreateRequest,
        egress_url: str,
        egress_auth: str,
    ) -> dict[str, Any]:
        """Map the customer's StreamCreateRequest to the gateway's
        session-open body shape."""
        return {
            "persona": {
                "name": req.persona.name,
                "system_prompt": req.persona.system_prompt,
            },
            "avatar": {"vrm_url": req.avatar.vrm_url},
            "voice": {"provider": req.voice.provider, "voice_id": req.voice.voice_id},
            "llm": {"model": req.llm.model},
            "render": {
                "width": req.render.width,
                "height": req.render.height,
                "fps": req.render.fps,
                "bitrate_bps": req.render.bitrate_bps,
            },
            "egress": {"url": egress_url, "auth": egress_auth},
        }

    async def aclose(self) -> None:
        # Best-effort cleanup of provider clients.
        for closeable in (self._gateway, self._egress, self._youtube):
            with contextlib.suppress(Exception):
                await closeable.close()
