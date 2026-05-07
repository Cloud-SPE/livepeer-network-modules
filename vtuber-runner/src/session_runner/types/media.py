"""Media-plane primitives: audio frames, encoded video frames, mux'd segments.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/types/media.py`.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class AudioFrame:
    pts_ms: int
    sample_rate_hz: int
    channels: int
    pcm_s16le: bytes


@dataclass(frozen=True)
class EncodedVideoFrame:
    pts_ms: int
    is_keyframe: bool
    h264_nalu: bytes


@dataclass(frozen=True)
class MuxedSegment:
    seq: int
    duration_ms: int
    container_bytes: bytes
