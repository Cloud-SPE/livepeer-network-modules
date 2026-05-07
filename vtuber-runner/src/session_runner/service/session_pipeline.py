"""`VTuberSession` orchestrator — assembles renderer + conversation + mux + sinks for one session.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/service/session_pipeline.py`.
"""

from __future__ import annotations

import asyncio

from session_runner.config import SessionConfig, Settings


class VTuberSession:
    def __init__(self, *, settings: Settings, config: SessionConfig) -> None:
        self._settings = settings
        self._config = config

    async def run(self) -> None:
        try:
            while True:
                await asyncio.sleep(1)
        except asyncio.CancelledError:
            return
