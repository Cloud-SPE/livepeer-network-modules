CREATE SCHEMA IF NOT EXISTS app;

CREATE TYPE app.customer_tier AS ENUM ('free', 'prepaid');
CREATE TYPE app.customer_status AS ENUM ('active', 'suspended', 'closed');
CREATE TYPE app.topup_status AS ENUM ('pending', 'succeeded', 'failed', 'refunded');
CREATE TYPE app.reservation_state AS ENUM ('open', 'committed', 'refunded');
CREATE TYPE app.reservation_kind AS ENUM ('prepaid', 'free');

CREATE TABLE app.customers (
  id                          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  email                       TEXT        NOT NULL UNIQUE,
  tier                        app.customer_tier NOT NULL,
  status                      app.customer_status NOT NULL DEFAULT 'active',
  rate_limit_tier             TEXT        NOT NULL DEFAULT 'default',
  created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  balance_usd_cents           BIGINT      NOT NULL DEFAULT 0,
  reserved_usd_cents          BIGINT      NOT NULL DEFAULT 0,
  quota_tokens_remaining      BIGINT,
  quota_monthly_allowance     BIGINT,
  quota_reserved_tokens       BIGINT      NOT NULL DEFAULT 0,
  quota_reset_at              TIMESTAMPTZ
);

CREATE TABLE app.api_keys (
  id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id   UUID        NOT NULL REFERENCES app.customers(id),
  hash          TEXT        NOT NULL,
  label         TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_used_at  TIMESTAMPTZ,
  revoked_at    TIMESTAMPTZ
);
CREATE INDEX api_key_hash_idx     ON app.api_keys (hash);
CREATE INDEX api_key_customer_idx ON app.api_keys (customer_id);

CREATE TABLE app.reservations (
  id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id         UUID        NOT NULL REFERENCES app.customers(id),
  work_id             TEXT        NOT NULL UNIQUE,
  kind                app.reservation_kind NOT NULL,
  amount_usd_cents    BIGINT,
  amount_tokens       BIGINT,
  state               app.reservation_state NOT NULL DEFAULT 'open',
  created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  resolved_at         TIMESTAMPTZ
);
CREATE INDEX reservation_customer_idx ON app.reservations (customer_id);

CREATE TABLE app.topups (
  id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id         UUID        NOT NULL REFERENCES app.customers(id),
  stripe_session_id   TEXT        NOT NULL UNIQUE,
  amount_usd_cents    BIGINT      NOT NULL,
  status              app.topup_status NOT NULL DEFAULT 'pending',
  created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  disputed_at         TIMESTAMPTZ,
  refunded_at         TIMESTAMPTZ
);
CREATE INDEX topup_customer_idx ON app.topups (customer_id);

CREATE TABLE app.stripe_webhook_events (
  event_id      TEXT        PRIMARY KEY,
  type          TEXT        NOT NULL,
  received_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  payload       TEXT        NOT NULL
);

CREATE TABLE app.admin_audit_events (
  id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  actor         TEXT        NOT NULL,
  action        TEXT        NOT NULL,
  target_id     TEXT,
  payload       TEXT,
  status_code   INTEGER     NOT NULL,
  occurred_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX admin_audit_event_actor_time_idx
  ON app.admin_audit_events (actor, occurred_at);
