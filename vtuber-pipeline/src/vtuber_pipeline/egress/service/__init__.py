"""Service layer: bearer minting + validation, ffmpeg supervision.

Bearer format (per ADR-007):
    pl_egress_<session-id>_<hex-hmac>
where `<hex-hmac>` is the lower-case hex digest of
HMAC-SHA256(pipeline_secret, session_id).
"""

from __future__ import annotations

import asyncio
import hashlib
import hmac
from collections.abc import AsyncIterator
from dataclasses import dataclass

import structlog

from vtuber_pipeline.egress.providers.ffmpeg_runner import FFmpegProcess, FFmpegRunner
from vtuber_pipeline.egress.repo import SecretsVault
from vtuber_pipeline.egress.types import (
    EgressBearer,
    SessionId,
    SessionRegistration,
)

_log = structlog.get_logger("egress.service")

_BEARER_PREFIX = "pl_egress_"


# ── bearer ────────────────────────────────────────────────────────────


def mint_bearer(session_id: SessionId, secret: bytes) -> str:
    """Build a `pl_egress_<sid>_<hmac>` bearer. Pipeline-side helper —
    in real deployments the gateway calls this on Pipeline's behalf and
    forwards the result to the worker."""
    digest = hmac.new(secret, session_id.encode(), hashlib.sha256).hexdigest()
    return f"{_BEARER_PREFIX}{session_id}_{digest}"


def parse_bearer(authorization_header: str) -> EgressBearer | None:
    """Pull the `pl_egress_*` token out of an `Authorization: Bearer …`
    header and split it into (session_id, hmac). Returns None for any
    malformed input."""
    if not authorization_header:
        return None
    parts = authorization_header.split(None, 1)
    if len(parts) != 2 or parts[0].lower() != "bearer":
        return None
    raw = parts[1].strip()
    if not raw.startswith(_BEARER_PREFIX):
        return None
    body = raw[len(_BEARER_PREFIX) :]
    sep = body.rfind("_")
    if sep <= 0:
        return None
    sid, hex_hmac = body[:sep], body[sep + 1 :]
    if not sid or not hex_hmac:
        return None
    return EgressBearer(raw=raw, session_id=sid, hmac_hex=hex_hmac)


def validate_bearer(bearer: EgressBearer, expected_session_id: SessionId, secret: bytes) -> bool:
    """Returns True iff the bearer's HMAC matches HMAC(secret, session_id)
    AND the bearer's session_id matches the URL's session_id. Uses a
    constant-time comparison."""
    if bearer.session_id != expected_session_id:
        return False
    expected = hmac.new(secret, expected_session_id.encode(), hashlib.sha256).hexdigest()
    return hmac.compare_digest(bearer.hmac_hex, expected)


# ── lifecycle ─────────────────────────────────────────────────────────


@dataclass
class EgressLifecycle:
    """Coordinates: bearer-validate, vault-lookup, spawn-ffmpeg, pump
    chunks, emit events, on disconnect: cleanup."""

    vault: SecretsVault
    ffmpeg: FFmpegRunner
    secret: bytes

    async def serve(
        self,
        session_id: SessionId,
        body_chunks: AsyncIterator[bytes],
    ) -> None:
        """Drives a single chunked-POST session. Caller has already:
            - validated the bearer (via `validate_bearer`),
            - claimed the in-flight flag (via vault.mark_in_flight),
        and will release the flag in their `finally` block.

        This method just spawns ffmpeg, pumps bytes from `body_chunks`
        into ffmpeg.stdin, and waits for ffmpeg to drain on disconnect.
        """
        reg = self.vault.get(session_id)
        if reg is None:
            # The check is also done at the route layer; this is a
            # safety belt in case the vault is revoked mid-flight.
            raise RuntimeError(f"no registration for session {session_id!r}")

        rtmp_target = _compose_rtmp_target(reg)
        # rtmp_target embeds the stream key; it must never be logged or
        # written to disk. We pass it to ffmpeg as a positional arg —
        # ffmpeg itself will not log it unless the operator passes
        # `-loglevel debug`, which this entrypoint never does.
        proc: FFmpegProcess = await self.ffmpeg.spawn(rtmp_target=rtmp_target)
        _emit("egress_ingesting", session_id=session_id)
        bytes_in = 0
        try:
            async for chunk in body_chunks:
                if not chunk:
                    continue
                bytes_in += len(chunk)
                await proc.write(chunk)
            await proc.close_stdin()
            rc = await proc.wait()
            _log.info(
                "egress_ffmpeg_exited",
                session_id=session_id,
                bytes_in=bytes_in,
                exit_code=rc,
            )
            if rc != 0:
                _emit("egress_errored", session_id=session_id, ffmpeg_exit_code=rc)
                raise RuntimeError(f"ffmpeg exited {rc}")
            _emit("egress_published", session_id=session_id, bytes_in=bytes_in)
        except asyncio.CancelledError:
            _log.info("egress_cancelled", session_id=session_id, bytes_in=bytes_in)
            await proc.terminate()
            raise
        except Exception:
            await proc.terminate()
            raise


def _compose_rtmp_target(reg: SessionRegistration) -> str:
    base = reg.rtmp_url.rstrip("/")
    return f"{base}/{reg.stream_key}"


def _emit(event: str, **kwargs: object) -> None:
    """Structured-event emit. Kept centralized so M6's redaction filter
    has a single place to plumb through."""
    _log.info(event, **kwargs)
