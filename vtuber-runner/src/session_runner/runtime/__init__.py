"""Runtime composition root for the session-runner FastAPI app."""

from __future__ import annotations

from session_runner.runtime.app import build_app
from session_runner.runtime.entrypoint import build, main
from session_runner.runtime.session_factory import SessionFactory

__all__ = ["build_app", "build", "main", "SessionFactory"]
