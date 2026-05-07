"""HTTP client to vtuber-gateway.

Talks the gateway's customer-facing API: POST /v1/vtuber/sessions to
open, customer-control WS for chat + events, (future) POST /:id/stop.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Protocol

import httpx


class GatewayError(RuntimeError):
    """Raised when the gateway returns a non-2xx or the call fails."""

    def __init__(self, message: str, *, status: int | None = None) -> None:
        super().__init__(message)
        self.status = status


@dataclass(frozen=True)
class GatewaySessionOpenResult:
    gateway_session_id: str
    customer_control_url: str
    session_child_bearer: str
    expires_at: str


class GatewayClient(Protocol):
    async def open_session(
        self,
        *,
        customer_bearer: str,
        params: dict[str, Any],
    ) -> GatewaySessionOpenResult: ...

    async def close(self) -> None: ...


class HTTPGatewayClient:
    """Concrete `GatewayClient` over httpx."""

    def __init__(
        self,
        *,
        base_url: str,
        timeout_secs: float = 30.0,
        client: httpx.AsyncClient | None = None,
    ) -> None:
        if not base_url:
            raise ValueError("HTTPGatewayClient requires a non-empty base_url")
        self._base_url = base_url.rstrip("/")
        self._timeout_secs = timeout_secs
        self._client = client
        self._owns_client = client is None

    def _ensure_client(self) -> httpx.AsyncClient:
        if self._client is None:
            self._client = httpx.AsyncClient(timeout=self._timeout_secs)
        return self._client

    async def open_session(
        self,
        *,
        customer_bearer: str,
        params: dict[str, Any],
    ) -> GatewaySessionOpenResult:
        url = f"{self._base_url}/v1/vtuber/sessions"
        resp = await self._ensure_client().post(
            url,
            headers={
                "Authorization": f"Bearer {customer_bearer}",
                "Content-Type": "application/json",
            },
            json=params,
        )
        if resp.status_code >= 300:
            raise GatewayError(
                f"gateway open_session HTTP {resp.status_code}: {resp.text[:512]}",
                status=resp.status_code,
            )
        body = resp.json()
        try:
            return GatewaySessionOpenResult(
                gateway_session_id=body["session_id"],
                customer_control_url=body["control_url"],
                session_child_bearer=body["session_child_bearer"],
                expires_at=body["expires_at"],
            )
        except KeyError as exc:
            raise GatewayError(f"gateway response missing field: {exc}") from exc

    async def close(self) -> None:
        if self._owns_client and self._client is not None:
            await self._client.aclose()
            self._client = None
