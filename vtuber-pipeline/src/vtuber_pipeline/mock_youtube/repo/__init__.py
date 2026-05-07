"""In-memory store for mock-youtube. No persistence — the dev compose
restarts the container if state needs clearing."""

from __future__ import annotations

import threading
from collections.abc import Iterator
from dataclasses import dataclass, field

from vtuber_pipeline.mock_youtube.types import LiveBroadcast, LiveStream


@dataclass
class _Store:
    broadcasts: dict[str, LiveBroadcast] = field(default_factory=dict)
    streams: dict[str, LiveStream] = field(default_factory=dict)
    lock: threading.RLock = field(default_factory=threading.RLock)


class BroadcastRepo:
    """Thread-safe in-memory broadcast/stream store. The state machine
    lives in `service`; this layer only owns persistence (such as it is)."""

    def __init__(self) -> None:
        self._s = _Store()

    # ── broadcasts ────────────────────────────────────────────────────

    def put_broadcast(self, b: LiveBroadcast) -> None:
        with self._s.lock:
            self._s.broadcasts[b.id] = b

    def get_broadcast(self, broadcast_id: str) -> LiveBroadcast | None:
        with self._s.lock:
            return self._s.broadcasts.get(broadcast_id)

    def list_broadcasts(self) -> list[LiveBroadcast]:
        with self._s.lock:
            return list(self._s.broadcasts.values())

    # ── streams ───────────────────────────────────────────────────────

    def put_stream(self, s: LiveStream) -> None:
        with self._s.lock:
            self._s.streams[s.id] = s

    def get_stream(self, stream_id: str) -> LiveStream | None:
        with self._s.lock:
            return self._s.streams.get(stream_id)

    def list_streams(self) -> list[LiveStream]:
        with self._s.lock:
            return list(self._s.streams.values())

    # ── iteration helpers (for the dashboard) ─────────────────────────

    def iter_active(self) -> Iterator[LiveBroadcast]:
        with self._s.lock:
            for b in self._s.broadcasts.values():
                if b.status.life_cycle_status in ("ready", "testing", "live"):
                    yield b
