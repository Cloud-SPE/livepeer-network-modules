"""HTTP routes for the mock YouTube Live Streaming API.

Endpoint shapes match the real API's URL/method/query-param contract so
a future swap to the real client is a base-URL flip:

  POST /youtube/v3/liveBroadcasts?part=snippet,status
  POST /youtube/v3/liveStreams?part=snippet,cdn
  POST /youtube/v3/liveBroadcasts/bind?id=<bid>&streamId=<sid>&part=...
  POST /youtube/v3/liveBroadcasts/transition?id=<bid>&broadcastStatus=<s>&part=...

Plus a small `/dashboard` page showing active broadcasts.
"""

# `Query(...)` in argument defaults is FastAPI's idiomatic dependency-
# injection pattern; ruff's B008 doesn't understand it. Skip it for
# this file.
# ruff: noqa: B008

from __future__ import annotations

from typing import Any

from fastapi import APIRouter, FastAPI, HTTPException, Query
from fastapi.responses import HTMLResponse, JSONResponse

from vtuber_pipeline.mock_youtube.repo import BroadcastRepo
from vtuber_pipeline.mock_youtube.service import BroadcastService
from vtuber_pipeline.mock_youtube.types import (
    BroadcastStatus,
    InsertBroadcastBody,
    InsertStreamBody,
)


def build_app(service: BroadcastService, repo: BroadcastRepo) -> FastAPI:
    app = FastAPI(title="mock-youtube", version="0.0.0")
    app.state.service = service
    app.state.repo = repo
    app.include_router(_v3_router(service))
    app.include_router(_health_router())
    app.include_router(_dashboard_router(repo))
    return app


# ── /youtube/v3 ───────────────────────────────────────────────────────


def _v3_router(service: BroadcastService) -> APIRouter:
    r = APIRouter(prefix="/youtube/v3", tags=["youtube-v3"])

    @r.post("/liveBroadcasts")
    async def insert_broadcast(
        body: InsertBroadcastBody,
        part: str = Query(default="snippet,status,contentDetails"),
    ) -> Any:
        del part  # accepted for API compat; we always return all parts
        b = service.insert_broadcast(body)
        return _dump(b)

    @r.post("/liveStreams")
    async def insert_stream(
        body: InsertStreamBody,
        part: str = Query(default="snippet,cdn"),
    ) -> Any:
        del part
        s = service.insert_stream(body)
        return _dump(s)

    @r.post("/liveBroadcasts/bind")
    async def bind(
        id: str = Query(...),
        streamId: str = Query(...),
        part: str = Query(default="id,contentDetails,status"),
    ) -> Any:
        del part
        b = service.bind(broadcast_id=id, stream_id=streamId)
        if b is None:
            raise HTTPException(status_code=404, detail="broadcast or stream not found")
        return _dump(b)

    @r.post("/liveBroadcasts/transition")
    async def transition(
        id: str = Query(...),
        broadcastStatus: BroadcastStatus = Query(...),
        part: str = Query(default="id,status"),
    ) -> Any:
        del part
        b = service.transition(broadcast_id=id, target=broadcastStatus)
        if b is None:
            raise HTTPException(status_code=404, detail="broadcast not found")
        return _dump(b)

    return r


# ── /api/health ───────────────────────────────────────────────────────


def _health_router() -> APIRouter:
    r = APIRouter(tags=["health"])

    @r.get("/api/health")
    async def health() -> Any:
        return {"status": "ok"}

    return r


# ── /dashboard ────────────────────────────────────────────────────────


def _dashboard_router(repo: BroadcastRepo) -> APIRouter:
    r = APIRouter(tags=["dashboard"])

    @r.get("/dashboard", response_class=HTMLResponse)
    async def dashboard() -> str:
        rows = []
        for b in repo.list_broadcasts():
            sid = b.content_details.bound_stream_id or ""
            stream = repo.get_stream(sid) if sid else None
            ingest = stream.cdn.ingestion_info.ingestion_address if stream else "(unbound)"
            # Stream key is bearer-equivalent — show only a fingerprint.
            key_repr = (
                _fingerprint(stream.cdn.ingestion_info.stream_name)
                if stream is not None
                else "(unbound)"
            )
            rows.append(
                f"<tr>"
                f"<td><code>{b.id}</code></td>"
                f"<td>{b.snippet.title or '(untitled)'}</td>"
                f"<td>{b.status.life_cycle_status}</td>"
                f"<td><code>{sid}</code></td>"
                f"<td><code>{ingest}</code></td>"
                f"<td><code>{key_repr}</code></td>"
                f"</tr>"
            )
        body = (
            "<html><head><title>mock-youtube dashboard</title>"
            "<style>"
            "body{font-family:-apple-system,sans-serif;margin:2em;color:#222}"
            "h1{font-size:1.4em} table{border-collapse:collapse;width:100%}"
            "th,td{padding:.4em .8em;border-bottom:1px solid #ddd;text-align:left}"
            "code{font-size:.9em;color:#555}"
            "</style></head><body>"
            "<h1>mock-youtube — broadcasts</h1>"
            "<table><thead><tr>"
            "<th>broadcastId</th><th>title</th><th>state</th>"
            "<th>streamId</th><th>ingestionAddress</th><th>streamKey (fp)</th>"
            "</tr></thead><tbody>"
            + ("".join(rows) if rows else "<tr><td colspan='6'><em>no broadcasts</em></td></tr>")
            + "</tbody></table></body></html>"
        )
        return body

    @r.get("/dashboard.json")
    async def dashboard_json() -> JSONResponse:
        out = []
        for b in repo.list_broadcasts():
            sid = b.content_details.bound_stream_id or None
            stream = repo.get_stream(sid) if sid else None
            out.append(
                {
                    "broadcastId": b.id,
                    "title": b.snippet.title,
                    "lifeCycleStatus": b.status.life_cycle_status,
                    "boundStreamId": sid,
                    "ingestionAddress": (
                        stream.cdn.ingestion_info.ingestion_address if stream else None
                    ),
                    "streamKeyFingerprint": (
                        _fingerprint(stream.cdn.ingestion_info.stream_name)
                        if stream is not None
                        else None
                    ),
                }
            )
        return JSONResponse({"broadcasts": out})

    return r


# ── helpers ───────────────────────────────────────────────────────────


def _dump(model: Any) -> Any:
    """Pydantic dump using YouTube's camelCase field names (via aliases)."""
    return model.model_dump(by_alias=True, mode="json")


def _fingerprint(stream_key: str) -> str:
    import hashlib

    return hashlib.sha256(stream_key.encode()).hexdigest()[:8]
