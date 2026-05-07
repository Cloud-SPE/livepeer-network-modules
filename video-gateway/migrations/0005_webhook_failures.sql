-- Plan 0013-video-followup phase 4 — dead-letter table for webhook
-- deliveries that exhaust the retry policy (3 retries with exponential
-- backoff: 1s / 5s / 30s). 4xx responses dead-letter immediately
-- (customer-error). Operators replay via admin SPA per §14 Q3 lock.

CREATE TABLE media.webhook_failures (
  id                 TEXT PRIMARY KEY,
  endpoint_id        TEXT NOT NULL REFERENCES media.webhook_endpoints(id) ON DELETE CASCADE,
  delivery_id        TEXT NOT NULL,
  event_type         TEXT NOT NULL,
  body               TEXT NOT NULL,
  signature_header   TEXT NOT NULL,
  attempt_count      INTEGER NOT NULL,
  last_error         TEXT NOT NULL,
  status_code        INTEGER,
  dead_lettered_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  replayed_at        TIMESTAMPTZ
);
CREATE INDEX webhook_failures_endpoint ON media.webhook_failures (endpoint_id);
CREATE INDEX webhook_failures_dead_lettered ON media.webhook_failures (dead_lettered_at);
