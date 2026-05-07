"""HTTP routes for the egress worker.

Exposes:

  POST /egress/sessions/{session_id}/media       — chunked-POST receiver
  POST /admin/sessions/{session_id}/register     — Pipeline registers (rtmp_url, stream_key)
  DELETE /admin/sessions/{session_id}            — Pipeline revokes session
  GET  /admin/sessions                           — list active sessions
  GET  /api/health                               — readiness probe

The chunked-POST receiver is the only endpoint the worker reaches; the
admin endpoints are Pipeline-internal (in production they'd be on a
separate listener with an admin-only ACL — for the dev mock we keep
them on the same port).
"""

from __future__ import annotations

from typing import Any

import structlog
from fastapi import APIRouter, FastAPI, HTTPException, Request
from pydantic import BaseModel, ConfigDict, Field

from vtuber_pipeline.egress.repo import SecretsVault
from vtuber_pipeline.egress.service import (
    EgressLifecycle,
    mint_bearer,
    parse_bearer,
    validate_bearer,
)
from vtuber_pipeline.egress.types import SessionLifeCycleStatus, SessionRegistration

_log = structlog.get_logger("egress.ui")


def build_app(
    vault: SecretsVault,
    lifecycle: EgressLifecycle,
    secret: bytes,
    public_base_url: str,
    admin_bearer: str = "",
) -> FastAPI:
    app = FastAPI(title="pipeline-egress", version="0.0.0")
    app.state.vault = vault
    app.state.lifecycle = lifecycle
    app.state.secret = secret
    app.state.public_base_url = public_base_url
    app.include_router(_media_router(vault, lifecycle, secret))
    app.include_router(_admin_router(vault, secret, public_base_url, admin_bearer))
    app.include_router(_health_router())
    return app


# ── /egress/sessions/{sid}/media ──────────────────────────────────────


def _media_router(
    vault: SecretsVault,
    lifecycle: EgressLifecycle,
    secret: bytes,
) -> APIRouter:
    r = APIRouter(prefix="/egress/sessions", tags=["egress"])

    @r.post("/{session_id}/media")
    async def receive_media(session_id: str, request: Request) -> Any:
        # 1. The bearer must parse, match the URL session-id, and HMAC-validate.
        bearer = parse_bearer(request.headers.get("Authorization", ""))
        if bearer is None:
            _log.warning("egress_bearer_malformed", session_id=session_id)
            raise HTTPException(status_code=401, detail="malformed bearer")
        if not validate_bearer(bearer, expected_session_id=session_id, secret=secret):
            _log.warning("egress_bearer_invalid", session_id=session_id)
            raise HTTPException(status_code=401, detail="invalid bearer")

        # 2. Vault must have a registration; without one we 404.
        if vault.get(session_id) is None:
            _log.warning("egress_no_registration", session_id=session_id)
            raise HTTPException(status_code=404, detail="session not registered")

        # 3. Single-use POST: claim the in-flight flag or reject 409.
        if not vault.mark_in_flight(session_id):
            _log.warning("egress_concurrent_post", session_id=session_id)
            raise HTTPException(status_code=409, detail="session already ingesting")

        _log.info("egress_chunked_post_opened", session_id=session_id)
        terminal_status: SessionLifeCycleStatus = "ended"
        err: str | None = None
        try:
            await lifecycle.serve(session_id=session_id, body_chunks=request.stream())
        except Exception as exc:
            terminal_status = "errored"
            err = type(exc).__name__
            _log.exception("egress_session_failed", session_id=session_id)
            raise HTTPException(status_code=500, detail="egress failed") from exc
        finally:
            vault.clear_in_flight(session_id, terminal_status=terminal_status, error=err)
        return {"status": "ok", "session_id": session_id}

    return r


# ── /admin/sessions ───────────────────────────────────────────────────


class _RegisterBody(BaseModel):
    model_config = ConfigDict(extra="ignore")
    rtmp_url: str = Field(..., description="e.g. rtmp://nginx-rtmp:1935/live")
    stream_key: str = Field(..., description="bearer-equivalent, never logged")


def _admin_router(
    vault: SecretsVault,
    secret: bytes,
    public_base_url: str,
    admin_bearer: str = "",
) -> APIRouter:
    r = APIRouter(prefix="/admin/sessions", tags=["admin"])

    def _check_admin_bearer(request: Request) -> None:
        """Reject requests without the configured admin bearer.

        When `admin_bearer` is empty (legacy default), allow all — keeps
        backward compat for in-cluster compose deployments. Operators
        running this listener on any externally-reachable port MUST set
        EGRESS_ADMIN_BEARER.
        """
        if not admin_bearer:
            return
        header = request.headers.get("Authorization", "")
        prefix = "Bearer "
        if not header.startswith(prefix) or header[len(prefix) :] != admin_bearer:
            raise HTTPException(status_code=401, detail="invalid admin bearer")

    @r.post("/{session_id}/register")
    async def register(session_id: str, body: _RegisterBody, request: Request) -> Any:
        _check_admin_bearer(request)
        vault.register(
            SessionRegistration(
                session_id=session_id,
                rtmp_url=body.rtmp_url,
                stream_key=body.stream_key,
            )
        )
        bearer = mint_bearer(session_id, secret)
        # We log the rtmp_url (operator-relevant) but never the stream_key.
        _log.info(
            "egress_session_registered",
            session_id=session_id,
            rtmp_url=body.rtmp_url,
        )
        return {
            "session_id": session_id,
            "egress_url": _compose_egress_url(public_base_url, session_id),
            "auth": f"Bearer {bearer}",
        }

    @r.delete("/{session_id}")
    async def revoke(session_id: str, request: Request) -> Any:
        _check_admin_bearer(request)
        vault.revoke(session_id)
        _log.info("egress_session_revoked", session_id=session_id)
        return {"session_id": session_id, "status": "revoked"}

    @r.get("")
    async def list_sessions(request: Request) -> Any:
        _check_admin_bearer(request)
        out = []
        for sid in vault.list_session_ids():
            state = vault.get_state(sid)
            out.append(
                {
                    "session_id": sid,
                    "status": state.status if state else "unknown",
                    "in_flight": state.in_flight if state else False,
                    "last_error": state.last_error if state else None,
                }
            )
        return {"sessions": out}

    return r


# ── /api/health ───────────────────────────────────────────────────────


def _health_router() -> APIRouter:
    r = APIRouter(tags=["health"])

    @r.get("/api/health")
    async def health() -> Any:
        return {"status": "ok"}

    return r


def _compose_egress_url(base: str, session_id: str) -> str:
    return f"{base.rstrip('/')}/egress/sessions/{session_id}/media"
