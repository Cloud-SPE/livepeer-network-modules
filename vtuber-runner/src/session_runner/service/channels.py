"""Inter-task pub/sub channels — fan-out audio + video frames between producers + sinks.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/service/channels.py`.
"""

from __future__ import annotations

import asyncio
from typing import Generic, TypeVar

T = TypeVar("T")


class FanoutChannel(Generic[T]):
    def __init__(self) -> None:
        self._subscribers: list[asyncio.Queue[T]] = []

    def subscribe(self, *, maxsize: int = 0) -> asyncio.Queue[T]:
        q: asyncio.Queue[T] = asyncio.Queue(maxsize=maxsize)
        self._subscribers.append(q)
        return q

    async def publish(self, item: T) -> None:
        for q in self._subscribers:
            try:
                q.put_nowait(item)
            except asyncio.QueueFull:
                pass
