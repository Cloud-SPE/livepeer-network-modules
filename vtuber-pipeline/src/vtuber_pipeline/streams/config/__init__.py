"""pipeline.streams runtime config — env-derived."""

from __future__ import annotations

import os
from dataclasses import dataclass


@dataclass(frozen=True)
class Settings:
    http_host: str = "0.0.0.0"
    http_port: int = 8092
    log_level: str = "INFO"
    log_format: str = "console"

    # Public base URL the customer hits (used to mint events_url back to them).
    public_base_url: str = "http://localhost:8092"

    # Bridge: where to open vtuber sessions. In-cluster URL.
    bridge_url: str = "http://vtuber-bridge:8080"
    # Customer-facing bridge URL — what customers use to subscribe to
    # the bridge's customer-control WS. Falls back to bridge_url.
    bridge_public_url: str = ""

    # The bridge customer Bearer key Pipeline uses on behalf of streams.
    # In production each stream may run under its own customer key; in
    # dev a single shared key is fine.
    bridge_customer_bearer: str = ""

    # Egress admin: where to register/revoke egress sessions.
    egress_admin_url: str = "http://egress:8091"
    egress_admin_bearer: str = ""

    # Default RTMP ingestion target (e.g. nginx-rtmp in dev). When a
    # YouTube broadcast is bound at create-time, this is overridden by
    # the YouTube ingest URL returned from the binder.
    default_rtmp_url: str = "rtmp://nginx-rtmp:1935/live"

    # YouTube binder: 'mock' (talks to pipeline.mock_youtube) | 'real'
    # | 'none' (skip; use default_rtmp_url with a synthetic stream key).
    youtube_backend: str = "none"
    mock_youtube_url: str = "http://mock-youtube:8090"

    @classmethod
    def from_env(cls) -> Settings:
        return cls(
            http_host=os.getenv("STREAMS_HTTP_HOST", cls.http_host),
            http_port=int(os.getenv("STREAMS_HTTP_PORT", str(cls.http_port))),
            log_level=os.getenv("STREAMS_LOG_LEVEL", cls.log_level),
            log_format=os.getenv("STREAMS_LOG_FORMAT", cls.log_format),
            public_base_url=os.getenv("STREAMS_PUBLIC_BASE_URL", cls.public_base_url),
            bridge_url=os.getenv("STREAMS_BRIDGE_URL", cls.bridge_url),
            bridge_public_url=os.getenv(
                "STREAMS_BRIDGE_PUBLIC_URL", os.getenv("STREAMS_BRIDGE_URL", cls.bridge_url)
            ),
            bridge_customer_bearer=os.getenv("STREAMS_BRIDGE_CUSTOMER_BEARER", ""),
            egress_admin_url=os.getenv("STREAMS_EGRESS_ADMIN_URL", cls.egress_admin_url),
            egress_admin_bearer=os.getenv("STREAMS_EGRESS_ADMIN_BEARER", ""),
            default_rtmp_url=os.getenv("STREAMS_DEFAULT_RTMP_URL", cls.default_rtmp_url),
            youtube_backend=os.getenv("STREAMS_YOUTUBE_BACKEND", cls.youtube_backend),
            mock_youtube_url=os.getenv("STREAMS_MOCK_YOUTUBE_URL", cls.mock_youtube_url),
        )
