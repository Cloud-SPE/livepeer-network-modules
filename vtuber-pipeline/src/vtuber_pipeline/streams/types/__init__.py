"""HTTP-boundary shapes + StreamRecord."""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from enum import StrEnum
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field


class StreamState(StrEnum):
    STARTING = "starting"
    LIVE = "live"
    STOPPING = "stopping"
    ENDED = "ended"
    ERRORED = "errored"


# ── customer-facing API DTOs ──────────────────────────────────────────


class _Persona(BaseModel):
    model_config = ConfigDict(extra="allow")
    name: str = Field(..., min_length=1, max_length=128)
    system_prompt: str = Field(..., min_length=1)


class _Avatar(BaseModel):
    model_config = ConfigDict(extra="allow")
    vrm_url: str = Field(..., min_length=1)


class _Voice(BaseModel):
    model_config = ConfigDict(extra="allow")
    provider: str
    voice_id: str


class _LLM(BaseModel):
    model_config = ConfigDict(extra="allow")
    model: str


class _Render(BaseModel):
    model_config = ConfigDict(extra="forbid")
    width: int = Field(..., ge=64, le=7680)
    height: int = Field(..., ge=64, le=4320)
    fps: int = Field(..., ge=1, le=120)
    bitrate_bps: int = Field(default=3_000_000, ge=100_000, le=100_000_000)


class YouTubeDestination(BaseModel):
    """Optional YouTube destination block. When present, the streams
    subapp creates a broadcast via the configured YouTube binder and
    binds the egress to YouTube's RTMP url."""

    model_config = ConfigDict(extra="forbid")

    title: str = Field(..., min_length=1, max_length=200)
    description: str = Field(default="", max_length=5000)
    privacy: Literal["public", "private", "unlisted"] = "private"


class StreamCreateRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")
    persona: _Persona
    avatar: _Avatar
    voice: _Voice
    llm: _LLM
    render: _Render
    youtube: YouTubeDestination | None = None


class StreamCreateResponse(BaseModel):
    stream_id: str
    state: StreamState
    events_url: str = Field(
        ..., description="Customer-side WS URL to subscribe for transcript + state events."
    )
    hls_preview_url: str | None = Field(
        default=None,
        description="Dev-only: nginx-rtmp HLS playlist URL. None in production.",
    )
    expires_at: datetime
    youtube_broadcast_id: str | None = None


class StreamGetResponse(BaseModel):
    stream_id: str
    state: StreamState
    started_at: datetime
    last_event_at: datetime | None = None
    error: str | None = None
    youtube_broadcast_id: str | None = None


class ChatSendRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")
    text: str = Field(..., min_length=1, max_length=10_000)


class ChatSendResponse(BaseModel):
    stream_id: str
    accepted: bool
    state: StreamState


class StreamStopResponse(BaseModel):
    stream_id: str
    state: StreamState


# ── internal record (in-memory; postgres-backed in production) ────────


@dataclass
class StreamRecord:
    """In-memory record of a stream's full state.

    Production deployments persist this to postgres so a Pipeline restart
    can recover in-flight streams. For the dev path we keep it in
    memory; restart-loses-state is acceptable.
    """

    stream_id: str
    state: StreamState
    started_at: datetime
    gateway_session_id: str
    gateway_session_child_bearer: str
    egress_session_id: str  # = stream_id by convention
    rtmp_url: str
    stream_key: str
    create_request: dict[str, Any]
    last_event_at: datetime | None = None
    error: str | None = None
    youtube_broadcast_id: str | None = None
    extra: dict[str, Any] = field(default_factory=dict)
