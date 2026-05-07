"""Work-units reporter — `SessionRunnerControl.ReportWorkUnits(stream)` server side.

Per plan 0013-vtuber OQ5 lock, the runner reports per-second monotonic deltas
over the gRPC bidi-stream the broker initiates on the runner-IPC socket
(`LIVEPEER_SESSION_RUNNER_SOCK`). Broker accumulates each delta into a
per-session `atomic.Uint64`; the broker's interim-debit ticker reads via
`LiveCounter.CurrentUnits()` (plan 0012-followup §8 + Q8 lock).

This module exposes a minimal `WorkUnitsReporter` that owns a monotonic
counter; the runner pushes increments here and a streaming-server adapter
(landed in a follow-up wiring commit) drains deltas to gRPC.
"""

from __future__ import annotations

import asyncio


class WorkUnitsReporter:
    def __init__(self) -> None:
        self._total: int = 0
        self._last_reported: int = 0
        self._lock = asyncio.Lock()

    async def add(self, units: int) -> None:
        if units <= 0:
            return
        async with self._lock:
            self._total += units

    async def take_delta(self) -> int:
        async with self._lock:
            delta = self._total - self._last_reported
            self._last_reported = self._total
            return delta

    @property
    def total(self) -> int:
        return self._total
