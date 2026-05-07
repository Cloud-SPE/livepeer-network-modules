-- Per-product rate-card row for /v1/images/generations. Lands alongside
-- the route + dispatcher in plan 0013-openai phase 4 (§14 OQ3 lock).
-- Pattern matching applies to model only; size + quality stay exact
-- match (matches the suite's shape).

CREATE TABLE IF NOT EXISTS app.rate_card_images (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  model_or_pattern  TEXT NOT NULL,
  is_pattern        BOOL NOT NULL,
  size              TEXT NOT NULL,
  quality           TEXT NOT NULL CHECK (quality IN ('standard','hd')),
  usd_per_image     NUMERIC(20, 8) NOT NULL CHECK (usd_per_image >= 0),
  sort_order        INT  NOT NULL DEFAULT 100,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (model_or_pattern, is_pattern, size, quality)
);
