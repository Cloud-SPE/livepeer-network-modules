"""In-memory StreamRepository for dev. Postgres-backed impl is a
follow-up; this surface is small enough that swapping is one file."""

from __future__ import annotations

import asyncio
from collections.abc import Iterable

from vtuber_pipeline.streams.types import StreamRecord


class StreamRepository:
    """In-memory store keyed by stream_id. Goroutine-safe via a single
    asyncio.Lock; that's enough for v1's ~5-concurrent target."""

    def __init__(self) -> None:
        self._records: dict[str, StreamRecord] = {}
        self._lock = asyncio.Lock()

    async def insert(self, record: StreamRecord) -> None:
        async with self._lock:
            if record.stream_id in self._records:
                raise KeyError(f"stream {record.stream_id} already exists")
            self._records[record.stream_id] = record

    async def get(self, stream_id: str) -> StreamRecord | None:
        async with self._lock:
            return self._records.get(stream_id)

    async def update(self, record: StreamRecord) -> None:
        async with self._lock:
            self._records[record.stream_id] = record

    async def remove(self, stream_id: str) -> StreamRecord | None:
        async with self._lock:
            return self._records.pop(stream_id, None)

    async def list_all(self) -> Iterable[StreamRecord]:
        async with self._lock:
            return list(self._records.values())
