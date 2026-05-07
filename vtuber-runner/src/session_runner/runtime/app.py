"""FastAPI factory for the session-runner.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/runtime/app.py`.
"""

from __future__ import annotations

from fastapi import FastAPI

from session_runner.config import Settings
from session_runner.service.manager import SessionManager
from session_runner.ui.http import build_router


def build_app(*, settings: Settings, manager: SessionManager) -> FastAPI:
    app = FastAPI(title="session-runner", version="0.1.0")

    @app.get("/api/health")
    async def health() -> dict[str, str]:
        return {"status": "ok"}

    @app.get("/options")
    async def options() -> dict[str, object]:
        return {
            "capabilities": ["livepeer:vtuber-session"],
            "renderer": settings.renderer,
        }

    app.include_router(build_router(manager))
    return app
