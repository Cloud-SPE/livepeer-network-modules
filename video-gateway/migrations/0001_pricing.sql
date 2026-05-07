-- Plan 0013-video phase 4 — per-resolution + per-codec pricing tables.
-- Live: cents-per-minute per resolution per tier.
-- VOD: cents-per-second per resolution per codec per tier.

CREATE TABLE media.pricing_live (
  id                TEXT PRIMARY KEY,
  resolution        TEXT NOT NULL,
  tier              TEXT NOT NULL,
  cents_per_minute  NUMERIC(14,6) NOT NULL,
  active            BOOLEAN NOT NULL DEFAULT TRUE,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX pricing_live_lookup ON media.pricing_live (resolution, tier, active);

CREATE TABLE media.pricing_vod (
  id                TEXT PRIMARY KEY,
  resolution        TEXT NOT NULL,
  codec             TEXT NOT NULL,
  tier              TEXT NOT NULL,
  cents_per_second  NUMERIC(14,6) NOT NULL,
  active            BOOLEAN NOT NULL DEFAULT TRUE,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX pricing_vod_lookup ON media.pricing_vod (resolution, codec, tier, active);

CREATE TABLE media.usage_records (
  id              TEXT PRIMARY KEY,
  project_id      TEXT NOT NULL,
  asset_id        TEXT REFERENCES media.assets(id) ON DELETE SET NULL,
  live_stream_id  TEXT REFERENCES media.live_streams(id) ON DELETE SET NULL,
  capability      TEXT NOT NULL,
  amount_cents    NUMERIC(14,6) NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX usage_records_project_created ON media.usage_records (project_id, created_at);
