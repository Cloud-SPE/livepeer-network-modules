"""Egress types.

The bearer format is `pl_egress_<session-id>_<hex-hmac>` per ADR-007.
The HMAC is sha256(session-id, pipeline_secret), hex-encoded.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Literal

SessionId = str

SessionLifeCycleStatus = Literal[
    "registered",  # vault entry exists, no chunked POST yet
    "ingesting",  # chunked POST open, ffmpeg consuming
    "ended",  # session closed cleanly
    "errored",  # ffmpeg crashed / unrecoverable disconnect
]


@dataclass(frozen=True)
class SessionRegistration:
    """The (rtmp_url, stream_key) tuple Pipeline writes to the egress
    vault before issuing a bearer. The worker uses this to compose the
    ffmpeg push target."""

    session_id: SessionId
    rtmp_url: str
    stream_key: str


@dataclass(frozen=True)
class EgressBearer:
    """Parsed bearer — the raw token plus its decomposed parts. We never
    expose this struct outside the service layer; it's a transient parse
    result."""

    raw: str
    session_id: SessionId
    hmac_hex: str


@dataclass
class SessionState:
    """Live state of one session."""

    session_id: SessionId
    status: SessionLifeCycleStatus = "registered"
    in_flight: bool = False
    """True while a chunked POST is open. Subsequent POSTs are rejected
    with 409 until this flips back to False on disconnect."""
    last_error: str | None = None
