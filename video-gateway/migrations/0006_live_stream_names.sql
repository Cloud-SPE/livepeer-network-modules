ALTER TABLE media.live_streams
  ADD COLUMN IF NOT EXISTS name TEXT;
