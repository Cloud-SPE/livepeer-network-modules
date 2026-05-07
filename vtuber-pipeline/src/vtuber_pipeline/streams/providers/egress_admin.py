"""HTTP client to pipeline.egress's /admin/sessions surface."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Protocol

import httpx


class EgressAdminError(RuntimeError):
    def __init__(self, message: str, *, status: int | None = None) -> None:
        super().__init__(message)
        self.status = status


@dataclass(frozen=True)
class EgressRegistration:
    session_id: str
    egress_url: str
    auth: str  # "Bearer pl_egress_<sid>_<hmac>"


class EgressAdminClient(Protocol):
    async def register(
        self, *, session_id: str, rtmp_url: str, stream_key: str
    ) -> EgressRegistration: ...

    async def revoke(self, *, session_id: str) -> None: ...

    async def close(self) -> None: ...


class HTTPEgressAdminClient:
    def __init__(
        self,
        *,
        base_url: str,
        admin_bearer: str = "",
        timeout_secs: float = 10.0,
        client: httpx.AsyncClient | None = None,
    ) -> None:
        if not base_url:
            raise ValueError("HTTPEgressAdminClient requires a non-empty base_url")
        self._base_url = base_url.rstrip("/")
        self._admin_bearer = admin_bearer
        self._timeout_secs = timeout_secs
        self._client = client
        self._owns_client = client is None

    def _headers(self) -> dict[str, str]:
        h = {"Content-Type": "application/json"}
        if self._admin_bearer:
            h["Authorization"] = f"Bearer {self._admin_bearer}"
        return h

    def _ensure_client(self) -> httpx.AsyncClient:
        if self._client is None:
            self._client = httpx.AsyncClient(timeout=self._timeout_secs)
        return self._client

    async def register(
        self, *, session_id: str, rtmp_url: str, stream_key: str
    ) -> EgressRegistration:
        url = f"{self._base_url}/admin/sessions/{session_id}/register"
        resp = await self._ensure_client().post(
            url,
            headers=self._headers(),
            json={"rtmp_url": rtmp_url, "stream_key": stream_key},
        )
        if resp.status_code >= 300:
            raise EgressAdminError(
                f"egress register HTTP {resp.status_code}: {resp.text[:256]}",
                status=resp.status_code,
            )
        body = resp.json()
        return EgressRegistration(
            session_id=body["session_id"],
            egress_url=body["egress_url"],
            auth=body["auth"],
        )

    async def revoke(self, *, session_id: str) -> None:
        url = f"{self._base_url}/admin/sessions/{session_id}"
        resp = await self._ensure_client().delete(url, headers=self._headers())
        if resp.status_code >= 300 and resp.status_code != 404:
            raise EgressAdminError(
                f"egress revoke HTTP {resp.status_code}: {resp.text[:256]}",
                status=resp.status_code,
            )

    async def close(self) -> None:
        if self._owns_client and self._client is not None:
            await self._client.aclose()
            self._client = None
