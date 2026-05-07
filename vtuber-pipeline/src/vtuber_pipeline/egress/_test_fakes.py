"""Test fakes for the egress worker.

Lives in the source tree (not tests/) because pytest's
`--import-mode=importlib` makes cross-test-module imports awkward, and
because these fakes are useful for downstream consumers (the
session-runner's integration tests, the umbrella compose harness, …)
that want to drive the egress lifecycle without spawning a real ffmpeg.

Underscored to keep it out of `from vtuber_pipeline.egress import *`.
"""

from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable

from vtuber_pipeline.egress.providers.ffmpeg_runner import FFmpegProcess, FFmpegRunner

WriteHook = Callable[["FakeFFmpegProcess", bytes], Awaitable[None]]


class FakeFFmpegProcess:
    """Scriptable fake. Default behavior is happy-path:
        - writes accepted, accumulating in `bytes_in`,
        - close_stdin → exit_event set → wait() returns 0.

    Tweak via the parent runner's `write_hook` and `exit_on_close`.
    """

    def __init__(self, runner: FakeFFmpegRunner, rtmp_target: str) -> None:
        self._runner = runner
        self.rtmp_target = rtmp_target
        self.bytes_in = 0
        self.terminated = False
        self.exit_code = 0
        self._stdin_open = True
        self._exit_event = asyncio.Event()

    async def write(self, chunk: bytes) -> None:
        if not self._stdin_open:
            raise RuntimeError("stdin closed")
        self.bytes_in += len(chunk)
        if self._runner.write_hook is not None:
            await self._runner.write_hook(self, chunk)

    async def close_stdin(self) -> None:
        self._stdin_open = False
        if self._runner.exit_on_close:
            self._exit_event.set()

    async def wait(self) -> int:
        if not self._exit_event.is_set():
            await self._exit_event.wait()
        return self.exit_code

    async def terminate(self) -> None:
        self.terminated = True
        self._exit_event.set()

    # ── test-only helpers ────────────────────────────────────────────

    def signal_exit(self, code: int = 0) -> None:
        """Simulate the ffmpeg process exiting on its own."""
        self.exit_code = code
        self._exit_event.set()


class FakeFFmpegRunner(FFmpegRunner):
    """Tracks every spawned process for assertions in tests."""

    def __init__(self) -> None:
        self.processes: list[FakeFFmpegProcess] = []
        self.exit_on_close: bool = True
        self.write_hook: WriteHook | None = None

    async def spawn(self, *, rtmp_target: str) -> FFmpegProcess:
        p = FakeFFmpegProcess(self, rtmp_target=rtmp_target)
        self.processes.append(p)
        return p


# ── ffmpeg that fails after N bytes ───────────────────────────────────


class FailingFFmpegProcess:
    """Fake that simulates an ffmpeg crash mid-stream — once the cumulative
    bytes_in crosses `fail_after_bytes`, the next write() raises and the
    process exits with code 1."""

    def __init__(self, *, fail_after_bytes: int) -> None:
        self.fail_after_bytes = fail_after_bytes
        self.bytes_in = 0
        self.terminated = False
        self.exit_code = 1
        self._stdin_open = True
        self._exit_event = asyncio.Event()

    async def write(self, chunk: bytes) -> None:
        if not self._stdin_open:
            raise RuntimeError("stdin closed")
        self.bytes_in += len(chunk)
        if self.bytes_in >= self.fail_after_bytes:
            self._stdin_open = False
            self._exit_event.set()
            raise RuntimeError("ffmpeg stdin closed unexpectedly")

    async def close_stdin(self) -> None:
        self._stdin_open = False
        self._exit_event.set()

    async def wait(self) -> int:
        await self._exit_event.wait()
        return self.exit_code

    async def terminate(self) -> None:
        self.terminated = True
        self._exit_event.set()


class FailingFFmpegRunner(FFmpegRunner):
    def __init__(self, *, fail_after_bytes: int) -> None:
        self.fail_after_bytes = fail_after_bytes
        self.processes: list[FailingFFmpegProcess] = []

    async def spawn(self, *, rtmp_target: str) -> FFmpegProcess:
        p = FailingFFmpegProcess(fail_after_bytes=self.fail_after_bytes)
        self.processes.append(p)
        return p
