-- vtuber-gateway initial schema. Per Q5 lock the gateway owns the
-- `vtuber.*` Postgres namespace; the shared customer-portal shell
-- owns `app.*` (incl. `app.customer`). Migrations folded from
-- livepeer-network-suite/livepeer-vtuber-gateway/migrations/
-- {0007_vtuber_sessions,0008_vtuber_session_bearers,
-- 0009_vtuber_session_payer_work_id}.sql per plan 0013-vtuber §5.1.

CREATE SCHEMA IF NOT EXISTS "vtuber";

DO $$ BEGIN
  CREATE TYPE "vtuber"."session_status" AS ENUM (
    'starting',
    'active',
    'ending',
    'ended',
    'errored'
  );
EXCEPTION
  WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS "vtuber"."session" (
  "id"                  uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
  "customer_id"         uuid NOT NULL,
  "status"              "vtuber"."session_status" DEFAULT 'starting' NOT NULL,
  "params_json"         text NOT NULL,
  "node_id"             text,
  "node_url"            text,
  "worker_session_id"   text,
  "control_url"         text NOT NULL,
  "expires_at"          timestamptz NOT NULL,
  "created_at"          timestamptz DEFAULT now() NOT NULL,
  "ended_at"            timestamptz,
  "error_code"          text,
  "payer_work_id"       text
);

DO $$ BEGIN
  ALTER TABLE "vtuber"."session"
    ADD CONSTRAINT "vtuber_session_customer_id_app_customer_id_fk"
    FOREIGN KEY ("customer_id") REFERENCES "app"."customer"("id")
    ON DELETE NO ACTION ON UPDATE NO ACTION;
EXCEPTION
  WHEN duplicate_object THEN NULL;
  WHEN undefined_table THEN NULL;
END $$;

CREATE INDEX IF NOT EXISTS "vtuber_session_customer_idx"
  ON "vtuber"."session" USING btree ("customer_id", "created_at");
CREATE INDEX IF NOT EXISTS "vtuber_session_status_idx"
  ON "vtuber"."session" USING btree ("status");
CREATE INDEX IF NOT EXISTS "vtuber_session_payer_work_id_idx"
  ON "vtuber"."session" USING btree ("payer_work_id");

CREATE TABLE IF NOT EXISTS "vtuber"."session_bearer" (
  "id"            uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
  "customer_id"   uuid NOT NULL,
  "session_id"    uuid NOT NULL,
  "hash"          text NOT NULL,
  "created_at"    timestamptz DEFAULT now() NOT NULL,
  "last_used_at"  timestamptz,
  "revoked_at"    timestamptz
);

DO $$ BEGIN
  ALTER TABLE "vtuber"."session_bearer"
    ADD CONSTRAINT "vtuber_session_bearer_customer_id_app_customer_id_fk"
    FOREIGN KEY ("customer_id") REFERENCES "app"."customer"("id")
    ON DELETE NO ACTION ON UPDATE NO ACTION;
EXCEPTION
  WHEN duplicate_object THEN NULL;
  WHEN undefined_table THEN NULL;
END $$;

DO $$ BEGIN
  ALTER TABLE "vtuber"."session_bearer"
    ADD CONSTRAINT "vtuber_session_bearer_session_id_vtuber_session_id_fk"
    FOREIGN KEY ("session_id") REFERENCES "vtuber"."session"("id")
    ON DELETE NO ACTION ON UPDATE NO ACTION;
EXCEPTION
  WHEN duplicate_object THEN NULL;
END $$;

CREATE INDEX IF NOT EXISTS "vtuber_session_bearer_hash_idx"
  ON "vtuber"."session_bearer" USING btree ("hash");
CREATE INDEX IF NOT EXISTS "vtuber_session_bearer_session_idx"
  ON "vtuber"."session_bearer" USING btree ("session_id");

CREATE TABLE IF NOT EXISTS "vtuber"."usage_record" (
  "id"           uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
  "session_id"   uuid NOT NULL,
  "customer_id"  uuid NOT NULL,
  "seconds"      integer NOT NULL,
  "cents"        bigint NOT NULL,
  "created_at"   timestamptz DEFAULT now() NOT NULL
);

DO $$ BEGIN
  ALTER TABLE "vtuber"."usage_record"
    ADD CONSTRAINT "vtuber_usage_record_session_id_vtuber_session_id_fk"
    FOREIGN KEY ("session_id") REFERENCES "vtuber"."session"("id")
    ON DELETE NO ACTION ON UPDATE NO ACTION;
EXCEPTION
  WHEN duplicate_object THEN NULL;
END $$;

CREATE INDEX IF NOT EXISTS "vtuber_usage_record_session_idx"
  ON "vtuber"."usage_record" USING btree ("session_id", "created_at");
CREATE INDEX IF NOT EXISTS "vtuber_usage_record_customer_idx"
  ON "vtuber"."usage_record" USING btree ("customer_id", "created_at");

CREATE TABLE IF NOT EXISTS "vtuber"."node_health" (
  "node_id"           text PRIMARY KEY NOT NULL,
  "node_url"          text NOT NULL,
  "last_success_at"   timestamptz,
  "last_failure_at"   timestamptz,
  "consecutive_fails" integer DEFAULT 0 NOT NULL,
  "circuit_open"      boolean DEFAULT false NOT NULL,
  "updated_at"        timestamptz DEFAULT now() NOT NULL
);

CREATE INDEX IF NOT EXISTS "vtuber_node_health_circuit_idx"
  ON "vtuber"."node_health" USING btree ("circuit_open", "updated_at");

CREATE TABLE IF NOT EXISTS "vtuber"."rate_card_session" (
  "offering"          text PRIMARY KEY NOT NULL,
  "usd_per_second"    numeric(12, 6) NOT NULL,
  "created_at"        timestamptz DEFAULT now() NOT NULL,
  "updated_at"        timestamptz DEFAULT now() NOT NULL
);
