"""Moderation types.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/providers/moderation/types.py`.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class ModerationVerdict:
    allow: bool
    reason: str | None = None
