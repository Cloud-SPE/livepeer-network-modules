// Daydream-portal-local tables. The customers / api_keys / auth_tokens
// tables live in @livepeer-network-modules/customer-portal — we only
// declare the things specific to this portal here: the waitlist (which
// gates customer creation), saved prompts (per-user scratchpad), and
// usage events (per-API-key session metering).

import {
  bigint,
  index,
  integer,
  pgTable,
  text,
  timestamp,
  uniqueIndex,
  uuid,
} from "drizzle-orm/pg-core";

export const waitlistStatus = ["pending", "approved", "rejected"] as const;
export type WaitlistStatus = (typeof waitlistStatus)[number];

export const waitlist = pgTable(
  "daydream_waitlist",
  {
    id: uuid("id").defaultRandom().primaryKey(),
    email: text("email").notNull(),
    displayName: text("display_name"),
    reason: text("reason"),
    status: text("status").$type<WaitlistStatus>().notNull().default("pending"),
    customerId: uuid("customer_id"),
    decidedBy: text("decided_by"),
    decidedAt: timestamp("decided_at", { withTimezone: true }),
    createdAt: timestamp("created_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
  },
  (t) => ({
    emailIdx: uniqueIndex("daydream_waitlist_email_idx").on(t.email),
    statusIdx: index("daydream_waitlist_status_idx").on(t.status),
  }),
);

export const savedPrompts = pgTable(
  "daydream_saved_prompts",
  {
    id: uuid("id").defaultRandom().primaryKey(),
    customerId: uuid("customer_id").notNull(),
    label: text("label").notNull(),
    body: text("body").notNull(),
    createdAt: timestamp("created_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
    updatedAt: timestamp("updated_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
  },
  (t) => ({
    customerIdx: index("daydream_saved_prompts_customer_idx").on(t.customerId),
  }),
);

export const usageEvents = pgTable(
  "daydream_usage_events",
  {
    id: uuid("id").defaultRandom().primaryKey(),
    customerId: uuid("customer_id").notNull(),
    apiKeyId: uuid("api_key_id"),
    sessionId: text("session_id").notNull(),
    orchestrator: text("orchestrator"),
    startedAt: timestamp("started_at", { withTimezone: true }).notNull(),
    endedAt: timestamp("ended_at", { withTimezone: true }),
    durationSeconds: integer("duration_seconds"),
    bytesIn: bigint("bytes_in", { mode: "bigint" }),
    bytesOut: bigint("bytes_out", { mode: "bigint" }),
    createdAt: timestamp("created_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
  },
  (t) => ({
    customerStartIdx: index("daydream_usage_events_customer_started_idx").on(
      t.customerId,
      t.startedAt,
    ),
    sessionIdx: uniqueIndex("daydream_usage_events_session_idx").on(
      t.sessionId,
    ),
  }),
);

export const daydreamSchema = {
  waitlist,
  savedPrompts,
  usageEvents,
};
