"""Console-script entrypoint: `pipeline-egress`."""

from __future__ import annotations

import logging
import sys

import structlog
import uvicorn
from fastapi import FastAPI

from vtuber_pipeline.egress.config import Settings
from vtuber_pipeline.egress.providers.ffmpeg_runner import AsyncioFFmpegRunner, FFmpegRunner
from vtuber_pipeline.egress.repo import SecretsVault
from vtuber_pipeline.egress.service import EgressLifecycle
from vtuber_pipeline.egress.ui import build_app


def build(
    settings: Settings | None = None,
    *,
    ffmpeg: FFmpegRunner | None = None,
) -> FastAPI:
    """Build the FastAPI app. Tests inject a fake `ffmpeg` runner; in
    production the default is `AsyncioFFmpegRunner` (real `ffmpeg`)."""
    settings = settings or Settings.from_env()
    _configure_logging(settings)
    vault = SecretsVault()
    runner = ffmpeg or AsyncioFFmpegRunner(binary=settings.ffmpeg_binary)
    lifecycle = EgressLifecycle(vault=vault, ffmpeg=runner, secret=settings.bearer_secret)
    return build_app(
        vault=vault,
        lifecycle=lifecycle,
        secret=settings.bearer_secret,
        public_base_url=settings.public_base_url,
        admin_bearer=settings.admin_bearer,
    )


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
    # Tests rebuild the app with different capture streams, so force
    # the stdlib handler to rebind instead of keeping the first stream.
    logging.basicConfig(level=level, format="%(message)s", stream=sys.stdout, force=True)
    renderer: structlog.types.Processor
    if settings.log_format == "json":
        renderer = structlog.processors.JSONRenderer()
    else:
        renderer = structlog.dev.ConsoleRenderer(colors=False)
    structlog.reset_defaults()
    structlog.configure(
        processors=[
            structlog.processors.add_log_level,
            structlog.processors.TimeStamper(fmt="iso"),
            renderer,
        ],
        logger_factory=structlog.PrintLoggerFactory(file=sys.stdout),
        wrapper_class=structlog.make_filtering_bound_logger(level),
        cache_logger_on_first_use=False,
    )


if __name__ == "__main__":
    main()
