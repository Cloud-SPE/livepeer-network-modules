"""structlog configuration helper.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/providers/telemetry.py`.
"""

from __future__ import annotations

import logging

import structlog


def configure_telemetry(*, level: str = "INFO", fmt: str = "console") -> None:
    log_level = getattr(logging, level.upper(), logging.INFO)
    logging.basicConfig(level=log_level, format="%(message)s")
    renderer: structlog.types.Processor
    if fmt == "json":
        renderer = structlog.processors.JSONRenderer()
    else:
        renderer = structlog.dev.ConsoleRenderer(colors=False)
    structlog.configure(
        processors=[
            structlog.processors.add_log_level,
            structlog.processors.TimeStamper(fmt="iso"),
            renderer,
        ],
        wrapper_class=structlog.make_filtering_bound_logger(log_level),
    )
