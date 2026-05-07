"""Static blocklist moderation.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/providers/moderation/blocklist.py`.
"""

from __future__ import annotations

from session_runner.providers.moderation.types import ModerationVerdict


class BlocklistModerator:
    def __init__(self, *, blocked_terms: tuple[str, ...] = ()) -> None:
        self._blocked = tuple(t.lower() for t in blocked_terms)

    def check(self, text: str) -> ModerationVerdict:
        if not text:
            return ModerationVerdict(allow=True)
        lowered = text.lower()
        for term in self._blocked:
            if term in lowered:
                return ModerationVerdict(allow=False, reason=f"blocked:{term}")
        return ModerationVerdict(allow=True)
