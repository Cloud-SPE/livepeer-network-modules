"""HTTP routes — `/api/sessions/*`.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/ui/http.py`.
"""

from __future__ import annotations

from datetime import datetime, timezone

from fastapi import APIRouter, HTTPException, status

from session_runner.service.manager import SessionManager
from session_runner.types.api import (
    SessionStartRequest,
    SessionStartResponse,
    SessionStatus,
    SessionStopResponse,
)


def build_router(manager: SessionManager) -> APIRouter:
    router = APIRouter(prefix="/api/sessions", tags=["sessions"])

    @router.post("/start", response_model=SessionStartResponse)
    async def start(req: SessionStartRequest) -> SessionStartResponse:
        record = await manager.start(req)
        return SessionStartResponse(
            session_id=record.state.session_id,
            status=record.state.status,
            started_at=record.state.started_at.isoformat(),
        )

    @router.post("/{session_id}/stop", response_model=SessionStopResponse)
    async def stop(session_id: str) -> SessionStopResponse:
        record = await manager.stop(session_id)
        if record is None:
            raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)
        return SessionStopResponse(
            session_id=record.state.session_id,
            status=record.state.status,
            stopped_at=datetime.now(timezone.utc).isoformat(),
        )

    @router.get("/{session_id}/status", response_model=SessionStatus)
    async def get_status(session_id: str) -> SessionStatus:
        record = manager.get(session_id)
        if record is None:
            raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)
        return SessionStatus(
            session_id=record.state.session_id,
            status=record.state.status,
            error_code=record.state.error_code,
            started_at=record.state.started_at.isoformat(),
            last_frame_at=(
                record.state.last_frame_at.isoformat()
                if record.state.last_frame_at is not None
                else None
            ),
        )

    return router
