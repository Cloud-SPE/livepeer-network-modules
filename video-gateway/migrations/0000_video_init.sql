-- Plan 0013-video phase 2 migration. Collapses suite's
-- livepeer-network-suite/livepeer-video-gateway/apps/api/drizzle/
--   0000_initial_schema.sql
--   0001_encoding_job_routes.sql
--   0002_assets_selected_offering.sql
--   0003_live_stream_pattern_b_fields.sql
-- into one initial migration. Lives in the media.* namespace per
-- plan 0013-video §14 Q3.

CREATE SCHEMA IF NOT EXISTS media;

CREATE TABLE media.projects (
  id          TEXT PRIMARY KEY,
  customer_id TEXT NOT NULL,
  name        TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE media.assets (
  id                TEXT PRIMARY KEY,
  project_id        TEXT NOT NULL,
  status            TEXT NOT NULL,
  source_type       TEXT NOT NULL,
  selected_offering TEXT,
  source_url        TEXT,
  duration_sec      NUMERIC(12,3),
  width             INTEGER,
  height            INTEGER,
  frame_rate        NUMERIC(6,3),
  audio_codec       TEXT,
  video_codec       TEXT,
  encoding_tier     TEXT NOT NULL,
  ffprobe_json      JSONB,
  error_message     TEXT,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  ready_at          TIMESTAMPTZ,
  deleted_at        TIMESTAMPTZ
);
CREATE INDEX assets_project_created ON media.assets (project_id, created_at);
CREATE INDEX assets_not_deleted     ON media.assets (deleted_at);

CREATE TABLE media.uploads (
  id            TEXT PRIMARY KEY,
  project_id    TEXT NOT NULL,
  asset_id      TEXT REFERENCES media.assets(id) ON DELETE CASCADE,
  status        TEXT NOT NULL,
  upload_url    TEXT NOT NULL,
  storage_key   TEXT NOT NULL,
  expires_at    TIMESTAMPTZ NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  completed_at  TIMESTAMPTZ
);

CREATE TABLE media.renditions (
  id           TEXT PRIMARY KEY,
  asset_id     TEXT NOT NULL REFERENCES media.assets(id) ON DELETE CASCADE,
  resolution   TEXT NOT NULL,
  codec        TEXT NOT NULL,
  bitrate_kbps INTEGER NOT NULL,
  storage_key  TEXT,
  status       TEXT NOT NULL,
  duration_sec NUMERIC(12,3),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  completed_at TIMESTAMPTZ,
  CONSTRAINT renditions_asset_resolution_codec UNIQUE (asset_id, resolution, codec)
);

CREATE TABLE media.encoding_jobs (
  id             TEXT PRIMARY KEY,
  asset_id       TEXT NOT NULL REFERENCES media.assets(id) ON DELETE CASCADE,
  rendition_id   TEXT REFERENCES media.renditions(id) ON DELETE CASCADE,
  kind           TEXT NOT NULL,
  status         TEXT NOT NULL,
  worker_url     TEXT,
  attempt_count  INTEGER NOT NULL DEFAULT 0,
  input_url      TEXT,
  output_prefix  TEXT,
  error_message  TEXT,
  started_at     TIMESTAMPTZ,
  completed_at   TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX encoding_jobs_status_created ON media.encoding_jobs (status, created_at);

CREATE TABLE media.live_streams (
  id                                 TEXT PRIMARY KEY,
  project_id                         TEXT NOT NULL,
  stream_key_hash                    TEXT NOT NULL UNIQUE,
  status                             TEXT NOT NULL,
  ingest_protocol                    TEXT NOT NULL,
  recording_enabled                  BOOLEAN NOT NULL DEFAULT FALSE,
  session_id                         TEXT,
  worker_id                          TEXT,
  worker_url                         TEXT,
  selected_capability                TEXT,
  selected_offering                  TEXT,
  selected_work_unit                 TEXT,
  selected_price_per_work_unit_wei   TEXT,
  payment_work_id                    TEXT,
  terminal_reason                    TEXT,
  recording_asset_id                 TEXT REFERENCES media.assets(id),
  last_seen_at                       TIMESTAMPTZ,
  created_at                         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  ended_at                           TIMESTAMPTZ
);

CREATE TABLE media.playback_ids (
  id              TEXT PRIMARY KEY,
  project_id      TEXT NOT NULL,
  asset_id        TEXT REFERENCES media.assets(id) ON DELETE CASCADE,
  live_stream_id  TEXT REFERENCES media.live_streams(id) ON DELETE CASCADE,
  policy          TEXT NOT NULL,
  token_required  BOOLEAN NOT NULL DEFAULT FALSE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT playback_ids_one_target CHECK ((asset_id IS NOT NULL) <> (live_stream_id IS NOT NULL))
);
