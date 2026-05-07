"""Import-smoke and surface-shape tests for the session-runner package."""

from __future__ import annotations

import session_runner
from session_runner import config, providers, runtime, service, types, ui  # noqa: F401
from session_runner.config import SessionConfig, Settings
from session_runner.providers import WorkUnitsReporter
from session_runner.types import SessionStartRequest, SessionStartResponse


def test_package_metadata() -> None:
    assert isinstance(session_runner.__version__, str)
    assert session_runner.__version__.count(".") >= 1


def test_settings_from_env_default(monkeypatch) -> None:
    for key in (
        "SESSION_RUNNER_HTTP_HOST",
        "SESSION_RUNNER_HTTP_PORT",
        "SESSION_RUNNER_LOG_LEVEL",
        "SESSION_RUNNER_RENDERER",
        "LIVEPEER_OPENAI_GATEWAY_URL",
        "LIVEPEER_OPENAI_GATEWAY_API_KEY",
        "TRICKLE_PUBLISH_URL",
        "WORKER_CONTROL_BEARER_PUBKEY",
    ):
        monkeypatch.delenv(key, raising=False)

    s = Settings.from_env()
    assert s.http_host == "0.0.0.0"
    assert s.http_port == 8080
    assert s.renderer == "chromium"


def test_session_config_basic() -> None:
    cfg = SessionConfig(
        session_id="abc",
        persona="grifter",
        vrm_url="https://example/vrm",
        llm_provider="livepeer",
        tts_provider="livepeer",
    )
    assert cfg.session_id == "abc"
    assert cfg.width == 1280


def test_session_start_request_validates() -> None:
    req = SessionStartRequest(
        session_id="s1",
        persona="grifter",
        vrm_url="https://example/vrm",
        llm_provider="livepeer",
        tts_provider="livepeer",
        worker_control_bearer="vtbsw_test",
    )
    assert req.session_id == "s1"
    assert req.target_fps == 24


def test_session_start_response_round_trip() -> None:
    resp = SessionStartResponse(
        session_id="s1",
        status="active",
        started_at="2026-05-07T00:00:00Z",
    )
    payload = resp.model_dump()
    assert payload["status"] == "active"
