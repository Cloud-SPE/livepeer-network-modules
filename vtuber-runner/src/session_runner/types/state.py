"""Internal session state.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/types/state.py`.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime, timezone

from session_runner.types.api import SessionStatusLiteral


@dataclass
class SessionState:
    session_id: str
    status: SessionStatusLiteral = "starting"
    error_code: str | None = None
    started_at: datetime = field(default_factory=lambda: datetime.now(timezone.utc))
    last_frame_at: datetime | None = None
    work_units_seq: int = 0


@dataclass
class SessionRecord:
    state: SessionState
    config_json: str
