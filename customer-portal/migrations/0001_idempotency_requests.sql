CREATE TYPE app.idempotency_state AS ENUM ('pending', 'completed');
CREATE TYPE app.idempotency_encoding AS ENUM ('utf8', 'base64');

CREATE TABLE app.idempotency_requests (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id UUID NOT NULL REFERENCES app.customers(id),
  idempotency_key TEXT NOT NULL,
  request_method TEXT NOT NULL,
  request_path TEXT NOT NULL,
  request_hash TEXT NOT NULL,
  state app.idempotency_state NOT NULL DEFAULT 'pending',
  response_status_code INTEGER,
  response_content_type TEXT,
  response_encoding app.idempotency_encoding,
  response_body TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX idempotency_requests_customer_key_uniq
  ON app.idempotency_requests (customer_id, idempotency_key);

CREATE INDEX idempotency_requests_created_at_idx
  ON app.idempotency_requests (created_at);
