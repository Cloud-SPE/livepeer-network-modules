"""Pydantic shapes for the YouTube Live Streaming API resources we mock.

Field names match the real API so a future swap to a real YouTube client
is a one-line change in the call sites. We only model the fields we touch.
"""

from __future__ import annotations

from datetime import datetime
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field

BroadcastStatus = Literal["created", "ready", "testing", "live", "complete", "revoked"]
PrivacyStatus = Literal["public", "unlisted", "private"]


class _ResourceBase(BaseModel):
    model_config = ConfigDict(extra="ignore", populate_by_name=True)
    kind: str = ""
    etag: str = ""
    id: str = ""


# ── liveBroadcast ──────────────────────────────────────────────────────


class BroadcastSnippet(BaseModel):
    model_config = ConfigDict(extra="ignore", populate_by_name=True)
    title: str = ""
    description: str = ""
    scheduled_start_time: datetime | None = Field(default=None, alias="scheduledStartTime")


class BroadcastStatusBlock(BaseModel):
    model_config = ConfigDict(extra="ignore", populate_by_name=True)
    privacy_status: PrivacyStatus = Field(default="unlisted", alias="privacyStatus")
    life_cycle_status: BroadcastStatus = Field(default="created", alias="lifeCycleStatus")


class BroadcastContentDetails(BaseModel):
    model_config = ConfigDict(extra="ignore", populate_by_name=True)
    bound_stream_id: str | None = Field(default=None, alias="boundStreamId")


class LiveBroadcast(_ResourceBase):
    kind: str = "youtube#liveBroadcast"
    snippet: BroadcastSnippet = Field(default_factory=BroadcastSnippet)
    status: BroadcastStatusBlock = Field(default_factory=BroadcastStatusBlock)
    content_details: BroadcastContentDetails = Field(
        default_factory=BroadcastContentDetails, alias="contentDetails"
    )


# ── liveStream ─────────────────────────────────────────────────────────


class IngestionInfo(BaseModel):
    """Real YouTube returns these inside `cdn.ingestionInfo`. The
    `streamName` is the per-broadcast stream key — bearer-equivalent."""

    model_config = ConfigDict(extra="ignore", populate_by_name=True)
    ingestion_address: str = Field(alias="ingestionAddress")
    stream_name: str = Field(alias="streamName")
    backup_ingestion_address: str = Field(default="", alias="backupIngestionAddress")
    rtmps_ingestion_address: str = Field(default="", alias="rtmpsIngestionAddress")


class StreamCdn(BaseModel):
    model_config = ConfigDict(extra="ignore", populate_by_name=True)
    format: str = "1080p"
    ingestion_type: str = Field(default="rtmp", alias="ingestionType")
    ingestion_info: IngestionInfo = Field(alias="ingestionInfo")


class LiveStream(_ResourceBase):
    kind: str = "youtube#liveStream"
    cdn: StreamCdn


# ── request bodies the caller sends to insert ─────────────────────────


class InsertBroadcastBody(BaseModel):
    model_config = ConfigDict(extra="ignore", populate_by_name=True)
    snippet: BroadcastSnippet = Field(default_factory=BroadcastSnippet)
    status: BroadcastStatusBlock = Field(default_factory=BroadcastStatusBlock)


class InsertStreamBody(BaseModel):
    model_config = ConfigDict(extra="ignore", populate_by_name=True)
    snippet: BroadcastSnippet = Field(default_factory=BroadcastSnippet)
    cdn: StreamCdn | None = None
