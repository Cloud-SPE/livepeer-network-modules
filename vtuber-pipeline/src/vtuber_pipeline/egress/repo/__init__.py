"""In-memory secrets vault + session state.

Per ADR-007, the (rtmp_url, stream_key) for a session is held in memory
only — never persisted, never logged. Acceptance criterion #7
(stream-key-never-persisted) is upheld here: this module never writes
to disk.
"""

from __future__ import annotations

import threading
from dataclasses import replace

from vtuber_pipeline.egress.types import (
    SessionId,
    SessionLifeCycleStatus,
    SessionRegistration,
    SessionState,
)


class SecretsVault:
    """Maps session-id → (rtmp_url, stream_key). In production, Pipeline's
    session-lifecycle service writes here when minting an egress URL.
    In dev/tests, the admin endpoint writes here.

    Thread-safe; backed by an RLock since reads + writes happen from
    both the request-handler coroutine and the ffmpeg supervisor."""

    def __init__(self) -> None:
        self._regs: dict[SessionId, SessionRegistration] = {}
        self._states: dict[SessionId, SessionState] = {}
        self._lock = threading.RLock()

    # ── registrations ────────────────────────────────────────────────

    def register(self, reg: SessionRegistration) -> None:
        with self._lock:
            self._regs[reg.session_id] = reg
            self._states.setdefault(
                reg.session_id, SessionState(session_id=reg.session_id, status="registered")
            )

    def get(self, session_id: SessionId) -> SessionRegistration | None:
        with self._lock:
            return self._regs.get(session_id)

    def revoke(self, session_id: SessionId) -> None:
        """Wipe the registration + state. Called on session end. Does NOT
        log the stream key."""
        with self._lock:
            self._regs.pop(session_id, None)
            self._states.pop(session_id, None)

    def list_session_ids(self) -> list[SessionId]:
        with self._lock:
            return list(self._regs.keys())

    # ── session state ────────────────────────────────────────────────

    def get_state(self, session_id: SessionId) -> SessionState | None:
        with self._lock:
            s = self._states.get(session_id)
            return replace(s) if s else None

    def mark_in_flight(self, session_id: SessionId) -> bool:
        """Atomically claim the in-flight flag. Returns False if the
        session already has an active chunked POST (caller should 409)."""
        with self._lock:
            s = self._states.get(session_id)
            if s is None:
                return False
            if s.in_flight:
                return False
            s.in_flight = True
            s.status = "ingesting"
            return True

    def clear_in_flight(
        self,
        session_id: SessionId,
        terminal_status: SessionLifeCycleStatus = "ended",
        error: str | None = None,
    ) -> None:
        with self._lock:
            s = self._states.get(session_id)
            if s is None:
                return
            s.in_flight = False
            s.status = terminal_status
            s.last_error = error
