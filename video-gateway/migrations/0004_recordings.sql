-- Plan 0013-video phase 4 — recording (live → recorded VOD) handoff
-- per §14 Q8 + OQ4 lock. Customer opts in via record_to_vod=true at
-- session-open; gateway creates a recording row that links the live
-- stream to the recorded asset once the session ends.

CREATE TABLE media.recordings (
  id              TEXT PRIMARY KEY,
  live_stream_id  TEXT NOT NULL REFERENCES media.live_streams(id) ON DELETE CASCADE,
  asset_id        TEXT REFERENCES media.assets(id) ON DELETE SET NULL,
  status          TEXT NOT NULL,
  started_at      TIMESTAMPTZ,
  ended_at        TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX recordings_stream ON media.recordings (live_stream_id);
