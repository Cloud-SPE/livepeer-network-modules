"""HTTP + WebSocket routes for pipeline.streams."""

from __future__ import annotations

import asyncio
import contextlib
import json

import structlog
import websockets
from fastapi import APIRouter, FastAPI, HTTPException, WebSocket, WebSocketDisconnect
from websockets.asyncio.client import connect as ws_connect

from vtuber_pipeline.streams.service import (
    StreamAlreadyEndedError,
    StreamLifecycle,
    StreamNotFoundError,
)
from vtuber_pipeline.streams.types import (
    ChatSendRequest,
    ChatSendResponse,
    StreamCreateRequest,
    StreamCreateResponse,
    StreamGetResponse,
    StreamState,
    StreamStopResponse,
)

_log = structlog.get_logger("streams.ui")


def build_app(*, lifecycle: StreamLifecycle | None, gateway_public_url: str) -> FastAPI:
    app = FastAPI(title="pipeline-streams", version="0.0.0")
    app.state.lifecycle = lifecycle
    app.state.gateway_public_url = gateway_public_url.rstrip("/")
    app.include_router(_streams_router(lifecycle, gateway_public_url))
    app.include_router(_health_router())
    return app


def _streams_router(lifecycle: StreamLifecycle | None, gateway_public_url: str) -> APIRouter:
    r = APIRouter(prefix="/api/streams", tags=["streams"])
    gateway_ws_url = gateway_public_url.replace("https://", "wss://").replace("http://", "ws://")

    @r.post("", response_model=StreamCreateResponse, status_code=201)
    async def create_stream(req: StreamCreateRequest) -> StreamCreateResponse:
        if lifecycle is None:
            raise HTTPException(
                status_code=503,
                detail={
                    "error": "not_configured",
                    "message": (
                        "STREAMS_GATEWAY_CUSTOMER_BEARER not set; recreate "
                        "this service with the bearer in env "
                        "(or run `make demo`)."
                    ),
                },
            )
        try:
            return await lifecycle.create(req)
        except Exception as exc:
            _log.exception("stream_create_failed")
            raise HTTPException(
                status_code=502,
                detail={"error": "create_failed", "message": str(exc)[:200]},
            ) from exc

    @r.get("/{stream_id}", response_model=StreamGetResponse)
    async def get_stream(stream_id: str) -> StreamGetResponse:
        if lifecycle is None:
            raise HTTPException(status_code=503, detail="not configured")
        try:
            rec = await lifecycle.get(stream_id)
        except StreamNotFoundError:
            raise HTTPException(status_code=404, detail="stream not found") from None
        return StreamGetResponse(
            stream_id=rec.stream_id,
            state=rec.state,
            started_at=rec.started_at,
            last_event_at=rec.last_event_at,
            error=rec.error,
            youtube_broadcast_id=rec.youtube_broadcast_id,
        )

    @r.post("/{stream_id}/chat", response_model=ChatSendResponse)
    async def send_chat(stream_id: str, body: ChatSendRequest) -> ChatSendResponse:
        if lifecycle is None:
            raise HTTPException(status_code=503, detail="not configured")
        try:
            rec = await lifecycle.get(stream_id)
        except StreamNotFoundError:
            raise HTTPException(status_code=404, detail="stream not found") from None
        if rec.state in (StreamState.ENDED, StreamState.ERRORED, StreamState.STOPPING):
            raise HTTPException(status_code=409, detail=f"stream {rec.state.value}")

        # One-shot: open WS, send chat, close. Events flow over the
        # /events WS (separate, persistent connection).
        ws_url = f"{gateway_ws_url}/v1/vtuber/sessions/{rec.gateway_session_id}/control"
        try:
            async with ws_connect(
                ws_url,
                additional_headers={"Authorization": f"Bearer {rec.gateway_session_child_bearer}"},
            ) as ws:
                await ws.send(json.dumps({"type": "user.chat.send", "data": {"text": body.text}}))
        except Exception as exc:
            _log.warning(
                "chat_relay_failed",
                stream_id=stream_id,
                error=type(exc).__name__,
            )
            raise HTTPException(status_code=502, detail="chat relay failed") from exc

        return ChatSendResponse(stream_id=stream_id, accepted=True, state=rec.state)

    @r.post("/{stream_id}/stop", response_model=StreamStopResponse)
    async def stop_stream(stream_id: str) -> StreamStopResponse:
        if lifecycle is None:
            raise HTTPException(status_code=503, detail="not configured")
        try:
            rec = await lifecycle.stop(stream_id)
        except StreamNotFoundError:
            raise HTTPException(status_code=404, detail="stream not found") from None
        except StreamAlreadyEndedError:
            raise HTTPException(status_code=409, detail="stream already ended") from None
        return StreamStopResponse(stream_id=rec.stream_id, state=rec.state)

    @r.websocket("/{stream_id}/events")
    async def stream_events(websocket: WebSocket, stream_id: str) -> None:
        await websocket.accept()
        if lifecycle is None:
            await websocket.close(code=1011, reason="not configured")
            return
        try:
            rec = await lifecycle.get(stream_id)
        except StreamNotFoundError:
            await websocket.close(code=1008, reason="stream not found")
            return

        gateway_ws = f"{gateway_ws_url}/v1/vtuber/sessions/{rec.gateway_session_id}/control"
        try:
            async with ws_connect(
                gateway_ws,
                additional_headers={"Authorization": f"Bearer {rec.gateway_session_child_bearer}"},
            ) as upstream:
                # Pump bidirectionally until either side closes.
                fwd_up = asyncio.create_task(_pump_ws_to_gateway(websocket, upstream))
                fwd_down = asyncio.create_task(_pump_gateway_to_ws(upstream, websocket))
                _done, pending = await asyncio.wait(
                    [fwd_up, fwd_down], return_when=asyncio.FIRST_COMPLETED
                )
                for t in pending:
                    t.cancel()
                    with contextlib.suppress(asyncio.CancelledError, Exception):
                        await t
        except websockets.exceptions.InvalidStatus as exc:
            _log.warning(
                "gateway_ws_rejected",
                stream_id=stream_id,
                status=exc.response.status_code,
            )
            await websocket.close(code=1011, reason="gateway ws unauthorized")
        except WebSocketDisconnect:
            pass
        except Exception:
            _log.exception("events_proxy_failed", stream_id=stream_id)
            with contextlib.suppress(Exception):
                await websocket.close(code=1011, reason="upstream error")

    return r


async def _pump_ws_to_gateway(customer: WebSocket, gateway: object) -> None:
    """Forward customer → gateway frames. `gateway` is a websockets
    ClientConnection; typed as object to avoid pulling websockets'
    internal types into our public surface."""
    while True:
        msg = await customer.receive_text()
        await gateway.send(msg)  # type: ignore[attr-defined]


async def _pump_gateway_to_ws(gateway: object, customer: WebSocket) -> None:
    """Forward gateway → customer frames."""
    async for msg in gateway:  # type: ignore[attr-defined]
        await customer.send_text(msg)


def _health_router() -> APIRouter:
    r = APIRouter(tags=["health"])

    @r.get("/api/health")
    async def health() -> dict[str, str]:
        return {"status": "ok"}

    return r
