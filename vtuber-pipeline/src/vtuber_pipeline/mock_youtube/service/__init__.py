"""Service layer: broadcast lifecycle + stream-key minting.

The state machine is intentionally lax — we accept any transition the
caller asks for as long as the broadcast exists. Real YouTube enforces
`created → ready → testing → live → complete`. We log warnings on
out-of-order transitions but do not reject them; the goal is a permissive
fake that doesn't get in the way of integration tests.
"""

from __future__ import annotations

import hashlib
import secrets as _secrets
import time
import uuid
from dataclasses import dataclass

import structlog

from vtuber_pipeline.mock_youtube.repo import BroadcastRepo
from vtuber_pipeline.mock_youtube.types import (
    BroadcastStatus,
    BroadcastStatusBlock,
    IngestionInfo,
    InsertBroadcastBody,
    InsertStreamBody,
    LiveBroadcast,
    LiveStream,
    StreamCdn,
)

_log = structlog.get_logger("mock_youtube.service")


@dataclass(frozen=True)
class StreamKeyPolicy:
    """Knobs for stream-key generation. Defaults match the dev profile."""

    ingestion_address: str = "rtmp://nginx-rtmp:1935/live"
    """Where the egress worker pushes the RTMP stream. In the umbrella
    compose this resolves to the nginx-rtmp container."""

    deterministic_seed: str | None = None
    """If set, stream keys are HMAC(seed, broadcast_id) — useful for tests
    that need reproducible keys. If None, keys are random."""


class BroadcastService:
    def __init__(self, repo: BroadcastRepo, policy: StreamKeyPolicy | None = None) -> None:
        self._repo = repo
        self._policy = policy or StreamKeyPolicy()

    # ── liveBroadcasts.insert ─────────────────────────────────────────

    def insert_broadcast(self, body: InsertBroadcastBody) -> LiveBroadcast:
        broadcast_id = self._mint_id("yt-bcast")
        b = LiveBroadcast(
            id=broadcast_id,
            etag=self._etag(broadcast_id),
            snippet=body.snippet,
            status=BroadcastStatusBlock(
                privacyStatus=body.status.privacy_status,
                lifeCycleStatus="created",
            ),
        )
        self._repo.put_broadcast(b)
        _log.info(
            "broadcast_inserted",
            broadcast_id=broadcast_id,
            title=body.snippet.title,
            privacy=body.status.privacy_status,
        )
        return b

    # ── liveStreams.insert ────────────────────────────────────────────

    def insert_stream(self, body: InsertStreamBody) -> LiveStream:
        stream_id = self._mint_id("yt-stream")
        stream_key = self._mint_stream_key(stream_id)
        cdn = StreamCdn(
            ingestionInfo=IngestionInfo(
                ingestionAddress=self._policy.ingestion_address,
                streamName=stream_key,
            )
        )
        s = LiveStream(id=stream_id, etag=self._etag(stream_id), cdn=cdn)
        self._repo.put_stream(s)
        _log.info(
            "stream_inserted",
            stream_id=stream_id,
            ingestion_address=self._policy.ingestion_address,
            # Stream key is bearer-equivalent — never log it. Only log a fingerprint.
            stream_key_fingerprint=hashlib.sha256(stream_key.encode()).hexdigest()[:8],
        )
        return s

    # ── liveBroadcasts.bind ───────────────────────────────────────────

    def bind(self, broadcast_id: str, stream_id: str) -> LiveBroadcast | None:
        b = self._repo.get_broadcast(broadcast_id)
        s = self._repo.get_stream(stream_id)
        if b is None or s is None:
            _log.warning(
                "bind_missing_resource",
                broadcast_id=broadcast_id,
                stream_id=stream_id,
                broadcast_found=b is not None,
                stream_found=s is not None,
            )
            return None
        b.content_details.bound_stream_id = stream_id
        # Per the API: bind moves the broadcast from `created` → `ready`.
        if b.status.life_cycle_status == "created":
            b.status.life_cycle_status = "ready"
        self._repo.put_broadcast(b)
        _log.info("broadcast_bound", broadcast_id=broadcast_id, stream_id=stream_id)
        return b

    # ── liveBroadcasts.transition ─────────────────────────────────────

    def transition(self, broadcast_id: str, target: BroadcastStatus) -> LiveBroadcast | None:
        b = self._repo.get_broadcast(broadcast_id)
        if b is None:
            _log.warning("transition_missing_broadcast", broadcast_id=broadcast_id)
            return None
        prev = b.status.life_cycle_status
        if not _valid_transition(prev, target):
            _log.warning(
                "transition_unusual",
                broadcast_id=broadcast_id,
                from_status=prev,
                to_status=target,
            )
        b.status.life_cycle_status = target
        self._repo.put_broadcast(b)
        _log.info(
            "broadcast_transitioned",
            broadcast_id=broadcast_id,
            from_status=prev,
            to_status=target,
        )
        return b

    # ── private ───────────────────────────────────────────────────────

    @staticmethod
    def _mint_id(prefix: str) -> str:
        return f"{prefix}-{uuid.uuid4().hex[:12]}"

    @staticmethod
    def _etag(seed: str) -> str:
        return hashlib.md5(f"{seed}-{time.time()}".encode()).hexdigest()

    def _mint_stream_key(self, stream_id: str) -> str:
        if self._policy.deterministic_seed is None:
            # Random 32-char key — same shape as a real YouTube stream key.
            return _secrets.token_hex(16)
        digest = hashlib.sha256(
            f"{self._policy.deterministic_seed}|{stream_id}".encode()
        ).hexdigest()
        return digest[:32]


# ── happy-path state-machine helper ───────────────────────────────────


_HAPPY_PATH = ["created", "ready", "testing", "live", "complete"]


def _valid_transition(prev: BroadcastStatus, target: BroadcastStatus) -> bool:
    """Returns True if `prev → target` follows the documented YouTube
    state machine. Unusual transitions are still permitted (we log a
    warning); this helper is for telemetry, not enforcement."""
    if target in ("complete", "revoked"):
        return True  # always allowed
    try:
        return _HAPPY_PATH.index(target) >= _HAPPY_PATH.index(prev)
    except ValueError:
        return False
