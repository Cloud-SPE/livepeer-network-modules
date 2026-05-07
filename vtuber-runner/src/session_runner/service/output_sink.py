"""Sink protocol — audio + video sinks share this surface.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/service/output_sink.py`.
"""

from __future__ import annotations

from typing import Protocol


class OutputSink(Protocol):
    async def start(self) -> None: ...
    async def stop(self) -> None: ...
