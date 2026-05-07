"""ffmpeg subprocess provider.

Spawns one ffmpeg process per session. Uses `-c copy` to avoid
re-encoding (the session-runner produces H.264+AAC at the target bitrate
already). Reads MP2T from stdin, pushes FLV over RTMP.

Three knobs the lifecycle layer cares about:
    - `spawn(rtmp_target)` → returns an `FFmpegProcess` handle.
    - `process.write(bytes)` → pushes a chunk into stdin.
    - `process.close_stdin()` + `process.wait()` → graceful shutdown.
    - `process.terminate()` → hard kill on cancellation.

Tests can swap the runner via dependency injection (the lifecycle takes
an `FFmpegRunner` and we pass a fake one in unit tests).
"""

from __future__ import annotations

import asyncio
import contextlib
import shutil
from dataclasses import dataclass
from typing import Protocol


class FFmpegProcess(Protocol):
    async def write(self, chunk: bytes) -> None: ...
    async def close_stdin(self) -> None: ...
    async def wait(self) -> int: ...
    async def terminate(self) -> None: ...


class FFmpegRunner(Protocol):
    async def spawn(self, *, rtmp_target: str) -> FFmpegProcess: ...


# ── real implementation ───────────────────────────────────────────────


@dataclass
class _AsyncioFFmpegProcess:
    proc: asyncio.subprocess.Process

    async def write(self, chunk: bytes) -> None:
        assert self.proc.stdin is not None
        if self.proc.stdin.is_closing():
            raise RuntimeError("ffmpeg stdin closed unexpectedly")
        self.proc.stdin.write(chunk)
        await self.proc.stdin.drain()

    async def close_stdin(self) -> None:
        if self.proc.stdin is not None and not self.proc.stdin.is_closing():
            self.proc.stdin.close()
            with contextlib.suppress(BrokenPipeError, ConnectionResetError):
                await self.proc.stdin.wait_closed()

    async def wait(self) -> int:
        return await self.proc.wait()

    async def terminate(self) -> None:
        if self.proc.returncode is not None:
            return
        try:
            self.proc.terminate()
            await asyncio.wait_for(self.proc.wait(), timeout=5)
        except TimeoutError:
            self.proc.kill()
            await self.proc.wait()


@dataclass
class AsyncioFFmpegRunner:
    """Production runner. Spawns `ffmpeg` from PATH (or `binary` override).

    Args:
        binary: ffmpeg executable path or name. Default: search PATH.
        extra_args: passed verbatim before `-c copy`. Useful for tuning.
    """

    binary: str = "ffmpeg"
    extra_args: tuple[str, ...] = ()

    async def spawn(self, *, rtmp_target: str) -> FFmpegProcess:
        binary = shutil.which(self.binary) or self.binary
        # The flag list mirrors docs/design-docs/egress-and-youtube.md §
        # "Egress worker architecture":
        #   -fflags +genpts        repair timestamps if the worker had a hiccup
        #   -f mpegts -i pipe:0    read MP2T from stdin
        #   -c copy                no re-encode (session-runner already produced H.264+AAC)
        #   -f flv <rtmp_target>   FLV-mux over RTMP
        cmd: list[str] = [
            binary,
            "-hide_banner",
            "-loglevel",
            "error",
            "-fflags",
            "+genpts",
            "-f",
            "mpegts",
            "-i",
            "pipe:0",
            "-c",
            "copy",
            *self.extra_args,
            "-f",
            "flv",
            rtmp_target,
        ]
        proc = await asyncio.create_subprocess_exec(
            *cmd,
            stdin=asyncio.subprocess.PIPE,
            stdout=asyncio.subprocess.DEVNULL,
            stderr=asyncio.subprocess.DEVNULL,
        )
        return _AsyncioFFmpegProcess(proc=proc)
