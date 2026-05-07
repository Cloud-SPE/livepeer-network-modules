import {
  bigint,
  boolean,
  index,
  integer,
  numeric,
  pgSchema,
  text,
  timestamp,
  uuid,
} from "drizzle-orm/pg-core";

// `vtuber.*` namespace per plan 0013-vtuber Q5 lock; the shared
// `app.*` namespace (incl. `app.customer`) is owned by
// `customer-portal/`. Source for these tables:
// `livepeer-network-suite/livepeer-vtuber-gateway/migrations/
// {0007,0008,0009}_*.sql` folded into one initial migration per
// plan 0013-vtuber §7.1.

export const vtuberSchema = pgSchema("vtuber");

export const vtuberSessionStatus = vtuberSchema.enum("session_status", [
  "starting",
  "active",
  "ending",
  "ended",
  "errored",
]);

export const vtuberSession = vtuberSchema.table(
  "session",
  {
    id: uuid("id").defaultRandom().primaryKey(),
    customerId: uuid("customer_id").notNull(),
    status: vtuberSessionStatus("status").default("starting").notNull(),
    paramsJson: text("params_json").notNull(),
    nodeId: text("node_id"),
    nodeUrl: text("node_url"),
    workerSessionId: text("worker_session_id"),
    controlUrl: text("control_url").notNull(),
    expiresAt: timestamp("expires_at", { withTimezone: true }).notNull(),
    createdAt: timestamp("created_at", { withTimezone: true })
      .defaultNow()
      .notNull(),
    endedAt: timestamp("ended_at", { withTimezone: true }),
    errorCode: text("error_code"),
    payerWorkId: text("payer_work_id"),
  },
  (t) => ({
    customerIdx: index("vtuber_session_customer_idx").on(
      t.customerId,
      t.createdAt,
    ),
    statusIdx: index("vtuber_session_status_idx").on(t.status),
    payerWorkIdIdx: index("vtuber_session_payer_work_id_idx").on(t.payerWorkId),
  }),
);

export const vtuberSessionBearer = vtuberSchema.table(
  "session_bearer",
  {
    id: uuid("id").defaultRandom().primaryKey(),
    customerId: uuid("customer_id").notNull(),
    sessionId: uuid("session_id").notNull(),
    hash: text("hash").notNull(),
    createdAt: timestamp("created_at", { withTimezone: true })
      .defaultNow()
      .notNull(),
    lastUsedAt: timestamp("last_used_at", { withTimezone: true }),
    revokedAt: timestamp("revoked_at", { withTimezone: true }),
  },
  (t) => ({
    hashIdx: index("vtuber_session_bearer_hash_idx").on(t.hash),
    sessionIdx: index("vtuber_session_bearer_session_idx").on(t.sessionId),
  }),
);

export const vtuberUsageRecord = vtuberSchema.table(
  "usage_record",
  {
    id: uuid("id").defaultRandom().primaryKey(),
    sessionId: uuid("session_id").notNull(),
    customerId: uuid("customer_id").notNull(),
    seconds: integer("seconds").notNull(),
    cents: bigint("cents", { mode: "bigint" }).notNull(),
    createdAt: timestamp("created_at", { withTimezone: true })
      .defaultNow()
      .notNull(),
  },
  (t) => ({
    sessionIdx: index("vtuber_usage_record_session_idx").on(
      t.sessionId,
      t.createdAt,
    ),
    customerIdx: index("vtuber_usage_record_customer_idx").on(
      t.customerId,
      t.createdAt,
    ),
  }),
);

export const vtuberNodeHealth = vtuberSchema.table(
  "node_health",
  {
    nodeId: text("node_id").primaryKey(),
    nodeUrl: text("node_url").notNull(),
    lastSuccessAt: timestamp("last_success_at", { withTimezone: true }),
    lastFailureAt: timestamp("last_failure_at", { withTimezone: true }),
    consecutiveFails: integer("consecutive_fails").default(0).notNull(),
    circuitOpen: boolean("circuit_open").default(false).notNull(),
    updatedAt: timestamp("updated_at", { withTimezone: true })
      .defaultNow()
      .notNull(),
  },
  (t) => ({
    circuitIdx: index("vtuber_node_health_circuit_idx").on(
      t.circuitOpen,
      t.updatedAt,
    ),
  }),
);

export const vtuberRateCardSession = vtuberSchema.table("rate_card_session", {
  offering: text("offering").primaryKey(),
  usdPerSecond: numeric("usd_per_second", { precision: 12, scale: 6 }).notNull(),
  createdAt: timestamp("created_at", { withTimezone: true })
    .defaultNow()
    .notNull(),
  updatedAt: timestamp("updated_at", { withTimezone: true })
    .defaultNow()
    .notNull(),
});
