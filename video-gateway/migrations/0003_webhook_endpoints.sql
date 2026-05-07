-- Plan 0013-video phase 4 — customer-configured webhook endpoints +
-- per-attempt delivery log (HMAC-SHA-256 signed per §14 Q7 lock).

CREATE TABLE media.webhook_endpoints (
  id            TEXT PRIMARY KEY,
  project_id    TEXT NOT NULL,
  url           TEXT NOT NULL,
  secret        TEXT NOT NULL,
  event_types   JSONB,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  disabled_at   TIMESTAMPTZ
);
CREATE INDEX webhook_endpoints_project ON media.webhook_endpoints (project_id);

CREATE TABLE media.webhook_deliveries (
  id            TEXT PRIMARY KEY,
  endpoint_id   TEXT NOT NULL REFERENCES media.webhook_endpoints(id) ON DELETE CASCADE,
  event_type    TEXT NOT NULL,
  body          TEXT NOT NULL,
  status        TEXT NOT NULL,
  attempts      INTEGER NOT NULL DEFAULT 0,
  last_error    TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  delivered_at  TIMESTAMPTZ
);
CREATE INDEX webhook_deliveries_status_created
  ON media.webhook_deliveries (status, created_at);
