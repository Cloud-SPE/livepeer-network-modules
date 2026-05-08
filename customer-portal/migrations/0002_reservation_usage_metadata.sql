ALTER TABLE app.reservations
  ADD COLUMN IF NOT EXISTS committed_usd_cents BIGINT,
  ADD COLUMN IF NOT EXISTS committed_tokens BIGINT,
  ADD COLUMN IF NOT EXISTS refunded_usd_cents BIGINT,
  ADD COLUMN IF NOT EXISTS refunded_tokens BIGINT,
  ADD COLUMN IF NOT EXISTS capability TEXT,
  ADD COLUMN IF NOT EXISTS model TEXT;
