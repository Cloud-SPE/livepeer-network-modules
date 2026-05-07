"""Console-script entrypoint: `pipeline-mock-youtube`.

Boots the FastAPI app under uvicorn. All configuration is env-driven
(see `pipeline.mock_youtube.config.Settings`).
"""

from __future__ import annotations

import logging

import structlog
import uvicorn
from fastapi import FastAPI

from vtuber_pipeline.mock_youtube.config import Settings
from vtuber_pipeline.mock_youtube.repo import BroadcastRepo
from vtuber_pipeline.mock_youtube.service import BroadcastService, StreamKeyPolicy
from vtuber_pipeline.mock_youtube.ui import build_app


def build(settings: Settings | None = None) -> FastAPI:
    settings = settings or Settings.from_env()
    _configure_logging(settings)
    repo = BroadcastRepo()
    policy = StreamKeyPolicy(
        ingestion_address=settings.rtmp_ingestion_address,
        deterministic_seed=settings.deterministic_seed,
    )
    service = BroadcastService(repo, policy=policy)
    return build_app(service, repo)


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
