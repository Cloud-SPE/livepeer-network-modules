"""Console-script entrypoint: `session-runner`.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/runtime/entrypoint.py`.
"""

from __future__ import annotations

import logging

import structlog
import uvicorn
from fastapi import FastAPI

from session_runner.config import Settings
from session_runner.runtime.app import build_app
from session_runner.runtime.session_factory import SessionFactory
from session_runner.service.manager import SessionManager


def build(settings: Settings | None = None) -> FastAPI:
    settings = settings or Settings.from_env()
    _configure_logging(settings)

    factory = SessionFactory(settings=settings)
    manager = SessionManager(factory=factory)
    return build_app(settings=settings, manager=manager)


def main() -> None:
    settings = Settings.from_env()
    app = build(settings)
    uvicorn.run(
        app,
        host=settings.http_host,
        port=settings.http_port,
        access_log=False,
    )


def _configure_logging(settings: Settings) -> None:
    level = getattr(logging, settings.log_level.upper(), logging.INFO)
    logging.basicConfig(level=level, format="%(message)s")
    renderer: structlog.types.Processor
    if settings.log_format == "json":
        renderer = structlog.processors.JSONRenderer()
    else:
        renderer = structlog.dev.ConsoleRenderer(colors=False)
    structlog.configure(
        processors=[
            structlog.processors.add_log_level,
            structlog.processors.TimeStamper(fmt="iso"),
            renderer,
        ],
        wrapper_class=structlog.make_filtering_bound_logger(level),
    )


if __name__ == "__main__":
    main()
