"""Session-factory: assembles a `VTuberSession` from config + providers.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/runtime/session_factory.py`.
"""

from __future__ import annotations

from session_runner.config import SessionConfig, Settings
from session_runner.service.session_pipeline import VTuberSession


class SessionFactory:
    def __init__(self, *, settings: Settings) -> None:
        self._settings = settings

    def build(self, session_config: SessionConfig) -> VTuberSession:
        return VTuberSession(settings=self._settings, config=session_config)
