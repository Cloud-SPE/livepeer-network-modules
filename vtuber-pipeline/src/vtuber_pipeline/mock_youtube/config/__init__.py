"""Mock-youtube runtime config — env-derived."""

from __future__ import annotations

import os
from dataclasses import dataclass


@dataclass(frozen=True)
class Settings:
    http_host: str = "0.0.0.0"
    http_port: int = 8090
    log_level: str = "INFO"
    log_format: str = "console"
    # `rtmp://nginx-rtmp:1935/live` in the umbrella compose; `localhost` for
    # bare-metal runs. The egress worker uses this to compose its RTMP push
    # target — `<ingestion_address>/<stream_key>`.
    rtmp_ingestion_address: str = "rtmp://nginx-rtmp:1935/live"
    # If set, stream keys are HMAC(seed, stream_id) — gives integration tests
    # a reproducible key without baking the value into the test.
    deterministic_seed: str | None = None

    @classmethod
    def from_env(cls) -> Settings:
        return cls(
            http_host=os.getenv("MOCK_YOUTUBE_HTTP_HOST", cls.http_host),
            http_port=int(os.getenv("MOCK_YOUTUBE_HTTP_PORT", str(cls.http_port))),
            log_level=os.getenv("MOCK_YOUTUBE_LOG_LEVEL", cls.log_level),
            log_format=os.getenv("MOCK_YOUTUBE_LOG_FORMAT", cls.log_format),
            rtmp_ingestion_address=os.getenv(
                "MOCK_YOUTUBE_RTMP_INGESTION_ADDRESS", cls.rtmp_ingestion_address
            ),
            deterministic_seed=os.getenv("MOCK_YOUTUBE_DETERMINISTIC_SEED") or None,
        )
