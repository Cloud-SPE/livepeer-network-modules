"""API request/response Pydantic models.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/types/api.py`.
"""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, Field

SessionStatusLiteral = Literal["starting", "active", "ending", "ended", "errored"]


class SessionStartRequest(BaseModel):
    session_id: str
    persona: str
    vrm_url: str
    llm_provider: str
    tts_provider: str
    target_youtube_broadcast: str | None = None
    width: int = Field(default=1280, ge=64, le=3840)
    height: int = Field(default=720, ge=64, le=2160)
    target_fps: int = Field(default=24, ge=1, le=60)
    worker_control_bearer: str
    extras: dict[str, str] = Field(default_factory=dict)


class SessionStartResponse(BaseModel):
    session_id: str
    status: SessionStatusLiteral
    started_at: str


class SessionStatus(BaseModel):
    session_id: str
    status: SessionStatusLiteral
    error_code: str | None = None
    started_at: str
    last_frame_at: str | None = None


class SessionStopResponse(BaseModel):
    session_id: str
    status: SessionStatusLiteral
    stopped_at: str
