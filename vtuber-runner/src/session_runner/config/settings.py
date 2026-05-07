"""Runner-process and per-session configuration.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/config/settings.py`.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field


@dataclass(frozen=True)
class Settings:
    http_host: str = "0.0.0.0"
    http_port: int = 8080
    log_level: str = "INFO"
    log_format: str = "console"
    renderer: str = "chromium"
    renderer_dist: str = "/app/avatar-renderer/dist"
    chromium_path: str | None = None
    livepeer_openai_gateway_url: str = ""
    livepeer_openai_gateway_api_key: str = ""
    olv_model_dir: str | None = None
    trickle_publish_url: str = ""
    worker_control_bearer_pubkey: str = ""
    runner_control_socket: str | None = None

    @classmethod
    def from_env(cls) -> "Settings":
        return cls(
            http_host=os.environ.get("SESSION_RUNNER_HTTP_HOST", "0.0.0.0"),
            http_port=int(os.environ.get("SESSION_RUNNER_HTTP_PORT", "8080")),
            log_level=os.environ.get("SESSION_RUNNER_LOG_LEVEL", "INFO"),
            log_format=os.environ.get("SESSION_RUNNER_LOG_FORMAT", "console"),
            renderer=os.environ.get("SESSION_RUNNER_RENDERER", "chromium"),
            renderer_dist=os.environ.get(
                "SESSION_RUNNER_RENDERER_DIST", "/app/avatar-renderer/dist"
            ),
            chromium_path=os.environ.get("CHROMIUM_PATH") or None,
            livepeer_openai_gateway_url=os.environ.get(
                "LIVEPEER_OPENAI_GATEWAY_URL", ""
            ),
            livepeer_openai_gateway_api_key=os.environ.get(
                "LIVEPEER_OPENAI_GATEWAY_API_KEY", ""
            ),
            olv_model_dir=os.environ.get("OLV_MODEL_DIR") or None,
            trickle_publish_url=os.environ.get("TRICKLE_PUBLISH_URL", ""),
            worker_control_bearer_pubkey=os.environ.get(
                "WORKER_CONTROL_BEARER_PUBKEY", ""
            ),
            runner_control_socket=os.environ.get("LIVEPEER_SESSION_RUNNER_SOCK")
            or None,
        )


@dataclass(frozen=True)
class SessionConfig:
    """Per-session config carried in the broker's POST /api/sessions/start body."""

    session_id: str
    persona: str
    vrm_url: str
    llm_provider: str
    tts_provider: str
    target_youtube_broadcast: str | None = None
    width: int = 1280
    height: int = 720
    target_fps: int = 24
    extras: dict[str, str] = field(default_factory=dict)
