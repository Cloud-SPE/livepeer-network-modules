-- Daydream-portal-local tables. customer-portal owns customers /
-- api_keys / auth_tokens / admin_audit_events; this migration adds
-- only the daydream-specific surfaces: waitlist (signup gating),
-- saved_prompts (per-user scratchpad), usage_events (per-session
-- metering, no credits/billing).

CREATE TABLE IF NOT EXISTS daydream_waitlist (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text NOT NULL,
    display_name  text,
    reason        text,
    status        text NOT NULL DEFAULT 'pending',
    customer_id   uuid,
    decided_by    text,
    decided_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT daydream_waitlist_status_chk
        CHECK (status IN ('pending', 'approved', 'rejected'))
);

CREATE UNIQUE INDEX IF NOT EXISTS daydream_waitlist_email_idx
    ON daydream_waitlist (email);

CREATE INDEX IF NOT EXISTS daydream_waitlist_status_idx
    ON daydream_waitlist (status);

CREATE TABLE IF NOT EXISTS daydream_saved_prompts (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id  uuid NOT NULL,
    label        text NOT NULL,
    body         text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS daydream_saved_prompts_customer_idx
    ON daydream_saved_prompts (customer_id);

CREATE TABLE IF NOT EXISTS daydream_usage_events (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id       uuid NOT NULL,
    api_key_id        uuid,
    session_id        text NOT NULL,
    orchestrator      text,
    started_at        timestamptz NOT NULL,
    ended_at          timestamptz,
    duration_seconds  integer,
    bytes_in          bigint,
    bytes_out         bigint,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS daydream_usage_events_customer_started_idx
    ON daydream_usage_events (customer_id, started_at);

CREATE UNIQUE INDEX IF NOT EXISTS daydream_usage_events_session_idx
    ON daydream_usage_events (session_id);
