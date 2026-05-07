-- Plan 0013-video phase 4 — per-second debit ledger for live streams.
-- Suite kept this in app.* (livepeer-network-suite/livepeer-video-gateway/
-- apps/api/drizzle/0000_initial_schema.sql line 80+); rewrite moves it to
-- media.* per §14 Q3 product-cohesion lock.

CREATE TABLE media.live_session_debits (
  id                  TEXT PRIMARY KEY,
  live_stream_id      TEXT NOT NULL REFERENCES media.live_streams(id) ON DELETE CASCADE,
  amount_usd_micros   BIGINT NOT NULL,
  duration_sec        INTEGER NOT NULL,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX live_session_debits_stream_created
  ON media.live_session_debits (live_stream_id, created_at);
