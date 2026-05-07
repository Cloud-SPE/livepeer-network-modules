"""Console-script entrypoint: `pipeline-streams`."""

from __future__ import annotations

import logging

import structlog
import uvicorn
from fastapi import FastAPI

from vtuber_pipeline.streams.config import Settings
from vtuber_pipeline.streams.providers import (
    HTTPBridgeClient,
    HTTPEgressAdminClient,
    MockYouTubeBinder,
    NoneYouTubeBinder,
    YouTubeBinder,
)
from vtuber_pipeline.streams.repo import StreamRepository
from vtuber_pipeline.streams.service import StreamLifecycle
from vtuber_pipeline.streams.ui import build_app


def _build_youtube(settings: Settings) -> YouTubeBinder:
    backend = settings.youtube_backend.lower()
    if backend == "mock":
        return MockYouTubeBinder(base_url=settings.mock_youtube_url)
    if backend == "real":
        raise NotImplementedError(
            "STREAMS_YOUTUBE_BACKEND=real not implemented; deferred to a "
            "follow-up plan. Use 'mock' or 'none'."
        )
    if backend == "none":
        return NoneYouTubeBinder(default_rtmp_url=settings.default_rtmp_url)
    raise ValueError(
        f"unknown STREAMS_YOUTUBE_BACKEND={backend!r}; expected 'mock' | 'real' | 'none'"
    )


def build(settings: Settings | None = None) -> FastAPI:
    settings = settings or Settings.from_env()
    _configure_logging(settings)

    repo = StreamRepository()
    bridge = HTTPBridgeClient(base_url=settings.bridge_url)
    egress = HTTPEgressAdminClient(
        base_url=settings.egress_admin_url,
        admin_bearer=settings.egress_admin_bearer,
    )
    youtube = _build_youtube(settings)

    # HLS preview template — only meaningful when default_rtmp_url
    # points at the dev nginx-rtmp. The {stream_key} placeholder lets
    # us hand customers a working preview URL in dev.
    hls_template: str | None = None
    if "nginx-rtmp" in settings.default_rtmp_url:
        hls_template = "http://localhost:8085/hls/{stream_key}.m3u8"

    # Boot tolerates an empty STREAMS_BRIDGE_CUSTOMER_BEARER so
    # `make dev-up` succeeds before a key is minted. The lifecycle
    # short-circuits at create-time with a clear error until set —
    # `make demo` provisions a key + recreates this service with
    # the bearer in env.
    if not settings.bridge_customer_bearer:
        import structlog

        structlog.get_logger("streams.entrypoint").warning(
            "STREAMS_BRIDGE_CUSTOMER_BEARER unset",
            hint="POST /api/streams will 503 until set; mint a key via "
            "vtuber-bridge's issueDevKey + recreate this service with the "
            "bearer in env (or run `make demo`).",
        )

    lifecycle: StreamLifecycle | None = None
    if settings.bridge_customer_bearer:
        lifecycle = StreamLifecycle(
            repo=repo,
            bridge=bridge,
            egress=egress,
            youtube=youtube,
            bridge_customer_bearer=settings.bridge_customer_bearer,
            bridge_public_url=settings.bridge_public_url or settings.bridge_url,
            public_base_url=settings.public_base_url,
            default_rtmp_url=settings.default_rtmp_url,
            hls_preview_template=hls_template,
        )

    return build_app(
        lifecycle=lifecycle,
        bridge_public_url=settings.bridge_public_url or settings.bridge_url,
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
