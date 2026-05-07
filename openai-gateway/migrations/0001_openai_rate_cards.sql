-- Per-product rate-card schema for the openai-gateway. Lives in the
-- shared app.* namespace (plan 0013-openai §14 Q1 — pragmatic exception:
-- the admin SPA's rate-card pages reference these tables and a schema
-- rename has no operator benefit). The file lives in openai-gateway/
-- migrations/ so per-product ownership stays explicit.
--
-- Tables:
--   - rate_card_chat_tiers        tier prices for chat (the only tiered capability)
--   - rate_card_chat_models       model -> tier (exact OR glob pattern)
--   - rate_card_embeddings        model -> USD/M tokens (exact OR pattern)
--   - rate_card_audio_speech      model -> USD/M chars (exact OR pattern)
--   - rate_card_audio_transcripts model -> USD/minute (exact OR pattern)
--
-- The rate_card_images row ships in a later migration alongside the
-- /v1/images/generations route (plan 0013-openai §14 OQ3 lock).
--
-- Pattern resolution at read time: exact match wins, then patterns in
-- sort_order ascending; first hit returns. The lookup helpers map a
-- null result onto a model-not-found error in the route handler.

CREATE SCHEMA IF NOT EXISTS app;

CREATE TABLE IF NOT EXISTS app.rate_card_chat_tiers (
  tier                    TEXT PRIMARY KEY
    CHECK (tier IN ('starter', 'standard', 'pro', 'premium')),
  input_usd_per_million   NUMERIC(20, 8) NOT NULL CHECK (input_usd_per_million  >= 0),
  output_usd_per_million  NUMERIC(20, 8) NOT NULL CHECK (output_usd_per_million >= 0),
  updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS app.rate_card_chat_models (
  id                  UUID  PRIMARY KEY DEFAULT gen_random_uuid(),
  model_or_pattern    TEXT  NOT NULL,
  is_pattern          BOOL  NOT NULL,
  tier                TEXT  NOT NULL
    REFERENCES app.rate_card_chat_tiers(tier)
    ON UPDATE CASCADE,
  sort_order          INT   NOT NULL DEFAULT 100,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (model_or_pattern, is_pattern)
);
CREATE INDEX IF NOT EXISTS rate_card_chat_models_exact_idx
  ON app.rate_card_chat_models (model_or_pattern) WHERE is_pattern = false;
CREATE INDEX IF NOT EXISTS rate_card_chat_models_patterns_idx
  ON app.rate_card_chat_models (sort_order) WHERE is_pattern = true;

CREATE TABLE IF NOT EXISTS app.rate_card_embeddings (
  id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  model_or_pattern         TEXT NOT NULL,
  is_pattern               BOOL NOT NULL,
  usd_per_million_tokens   NUMERIC(20, 8) NOT NULL CHECK (usd_per_million_tokens >= 0),
  sort_order               INT  NOT NULL DEFAULT 100,
  created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (model_or_pattern, is_pattern)
);

CREATE TABLE IF NOT EXISTS app.rate_card_audio_speech (
  id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  model_or_pattern         TEXT NOT NULL,
  is_pattern               BOOL NOT NULL,
  usd_per_million_chars    NUMERIC(20, 8) NOT NULL CHECK (usd_per_million_chars >= 0),
  sort_order               INT  NOT NULL DEFAULT 100,
  created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (model_or_pattern, is_pattern)
);

CREATE TABLE IF NOT EXISTS app.rate_card_audio_transcripts (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  model_or_pattern  TEXT NOT NULL,
  is_pattern        BOOL NOT NULL,
  usd_per_minute    NUMERIC(20, 8) NOT NULL CHECK (usd_per_minute >= 0),
  sort_order        INT  NOT NULL DEFAULT 100,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (model_or_pattern, is_pattern)
);
