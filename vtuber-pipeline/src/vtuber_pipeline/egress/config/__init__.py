"""Egress runtime config — env-derived."""

from __future__ import annotations

import os
from dataclasses import dataclass


@dataclass(frozen=True)
class Settings:
    http_host: str = "0.0.0.0"
    http_port: int = 8091
    log_level: str = "INFO"
    log_format: str = "console"

    # External base URL — used when minting the egress URL Pipeline hands
    # to the worker. Must match how the worker reaches this service.
    public_base_url: str = "http://egress:8091"

    # HMAC secret. Must be at least 32 bytes; the entrypoint refuses to
    # boot with a default in non-dev mode.
    bearer_secret: bytes = b"dev-secret-change-me-dev-secret!"

    # Admin bearer for the /admin/sessions/* surface. When set, requests
    # to the admin endpoints must carry `Authorization: Bearer <token>`.
    # When empty (legacy + dev-default), the admin surface is open —
    # acceptable for a pipeline-internal compose, NOT for any deployment
    # that exposes /admin/* to anything other than the pipeline's own
    # network. Set EGRESS_ADMIN_BEARER in production.
    admin_bearer: str = ""

    # Path to the ffmpeg binary. Default lets PATH lookup decide.
    ffmpeg_binary: str = "ffmpeg"

    @classmethod
    def from_env(cls) -> Settings:
        secret_raw = os.getenv("EGRESS_BEARER_SECRET")
        secret = secret_raw.encode() if secret_raw else cls.bearer_secret
        return cls(
            http_host=os.getenv("EGRESS_HTTP_HOST", cls.http_host),
            http_port=int(os.getenv("EGRESS_HTTP_PORT", str(cls.http_port))),
            log_level=os.getenv("EGRESS_LOG_LEVEL", cls.log_level),
            log_format=os.getenv("EGRESS_LOG_FORMAT", cls.log_format),
            public_base_url=os.getenv("EGRESS_PUBLIC_BASE_URL", cls.public_base_url),
            bearer_secret=secret,
            admin_bearer=os.getenv("EGRESS_ADMIN_BEARER", cls.admin_bearer),
            ffmpeg_binary=os.getenv("EGRESS_FFMPEG_BINARY", cls.ffmpeg_binary),
        )
