CREATE TYPE app.auth_token_kind AS ENUM ('customer_ui');

CREATE TABLE app.auth_tokens (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id   UUID NOT NULL REFERENCES app.customers(id),
  kind          app.auth_token_kind NOT NULL DEFAULT 'customer_ui',
  hash          TEXT NOT NULL,
  label         TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_used_at  TIMESTAMPTZ,
  revoked_at    TIMESTAMPTZ
);

CREATE INDEX auth_token_hash_idx ON app.auth_tokens (hash);
CREATE INDEX auth_token_customer_idx ON app.auth_tokens (customer_id);
