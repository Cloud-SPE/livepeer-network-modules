"""Session-scoped bearer HTTP client — wraps httpx with the worker-control bearer header.

The bearer is minted by `vtuber-gateway` (HMAC-SHA-256 with pepper, hash-stored)
per plan 0013-vtuber Q8 lock and passed to the runner in the
`POST /api/sessions/start` body. The runner uses it to dial back outbound services
(LLM/TTS via openai-gateway, etc.).
"""

from __future__ import annotations

import httpx


class SessionBearerHTTPClient:
    def __init__(self, *, base_url: str, bearer: str, timeout_secs: float = 30.0) -> None:
        if not base_url:
            raise ValueError("SessionBearerHTTPClient requires a non-empty base_url")
        if not bearer:
            raise ValueError("SessionBearerHTTPClient requires a non-empty bearer")
        self._base_url = base_url.rstrip("/")
        self._bearer = bearer
        self._client = httpx.AsyncClient(
            base_url=self._base_url,
            timeout=timeout_secs,
            headers={"Authorization": f"Bearer {bearer}"},
        )

    @property
    def client(self) -> httpx.AsyncClient:
        return self._client

    async def aclose(self) -> None:
        await self._client.aclose()
