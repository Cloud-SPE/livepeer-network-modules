"""Top-level `SessionManager` — owns the in-memory session table.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/service/manager.py`.
"""

from __future__ import annotations

import asyncio

from session_runner.config import SessionConfig
from session_runner.runtime.session_factory import SessionFactory
from session_runner.types.api import SessionStartRequest
from session_runner.types.state import SessionRecord, SessionState


class SessionManager:
    def __init__(self, *, factory: SessionFactory) -> None:
        self._factory = factory
        self._sessions: dict[str, SessionRecord] = {}
        self._tasks: dict[str, asyncio.Task[None]] = {}
        self._lock = asyncio.Lock()

    async def start(self, req: SessionStartRequest) -> SessionRecord:
        async with self._lock:
            if req.session_id in self._sessions:
                return self._sessions[req.session_id]

            config = SessionConfig(
                session_id=req.session_id,
                persona=req.persona,
                vrm_url=req.vrm_url,
                llm_provider=req.llm_provider,
                tts_provider=req.tts_provider,
                target_youtube_broadcast=req.target_youtube_broadcast,
                width=req.width,
                height=req.height,
                target_fps=req.target_fps,
                extras=req.extras,
            )
            session = self._factory.build(config)
            state = SessionState(session_id=req.session_id, status="active")
            record = SessionRecord(state=state, config_json=config.session_id)
            self._sessions[req.session_id] = record
            self._tasks[req.session_id] = asyncio.create_task(session.run())
            return record

    async def stop(self, session_id: str) -> SessionRecord | None:
        async with self._lock:
            record = self._sessions.get(session_id)
            if record is None:
                return None
            task = self._tasks.pop(session_id, None)
            record.state.status = "ending"

        if task is not None:
            task.cancel()
            try:
                await task
            except (asyncio.CancelledError, Exception):
                pass

        record.state.status = "ended"
        return record

    def get(self, session_id: str) -> SessionRecord | None:
        return self._sessions.get(session_id)
