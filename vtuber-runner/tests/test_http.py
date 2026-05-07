"""HTTP route smoke tests — `/api/health`, `/options`, `/api/sessions/*`."""

from __future__ import annotations

from fastapi.testclient import TestClient

from session_runner.config import Settings
from session_runner.runtime.app import build_app
from session_runner.runtime.session_factory import SessionFactory
from session_runner.service.manager import SessionManager


def _client() -> TestClient:
    settings = Settings()
    factory = SessionFactory(settings=settings)
    manager = SessionManager(factory=factory)
    return TestClient(build_app(settings=settings, manager=manager))


def test_health_endpoint_returns_ok() -> None:
    with _client() as c:
        resp = c.get("/api/health")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ok"}


def test_options_lists_vtuber_session_capability() -> None:
    with _client() as c:
        resp = c.get("/options")
    assert resp.status_code == 200
    body = resp.json()
    assert "livepeer:vtuber-session" in body["capabilities"]


def test_session_status_404_for_unknown_id() -> None:
    with _client() as c:
        resp = c.get("/api/sessions/unknown/status")
    assert resp.status_code == 404
