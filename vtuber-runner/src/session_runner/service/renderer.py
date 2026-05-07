"""Renderer-driver façade — abstract base for chromium / fixture backends.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/service/renderer.py`.
"""

from __future__ import annotations

from typing import Protocol

from session_runner.types.media import EncodedVideoFrame


class Renderer(Protocol):
    async def start(self) -> None: ...
    async def stop(self) -> None: ...
    async def frames(self) -> "AsyncIterator[EncodedVideoFrame]": ...  # type: ignore[name-defined]


from collections.abc import AsyncIterator  # noqa: E402  (Protocol forward ref)

__all__ = ["AsyncIterator", "Renderer"]
