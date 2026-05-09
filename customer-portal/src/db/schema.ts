import {
  bigint,
  index,
  integer,
  pgEnum,
  pgSchema,
  text,
  timestamp,
  uniqueIndex,
  uuid,
} from 'drizzle-orm/pg-core';

export const appSchema = pgSchema('app');

export const customerTier = pgEnum('customer_tier', ['free', 'prepaid']);
export const customerStatus = pgEnum('customer_status', ['active', 'suspended', 'closed']);
export const topupStatus = pgEnum('topup_status', ['pending', 'succeeded', 'failed', 'refunded']);
export const reservationState = pgEnum('reservation_state', ['open', 'committed', 'refunded']);
export const reservationKind = pgEnum('reservation_kind', ['prepaid', 'free']);
export const authTokenKind = pgEnum('auth_token_kind', ['customer_ui']);

export const customers = appSchema.table('customers', {
  id: uuid('id').primaryKey().defaultRandom(),
  email: text('email').notNull().unique(),
  tier: customerTier('tier').notNull(),
  status: customerStatus('status').notNull().default('active'),
  rateLimitTier: text('rate_limit_tier').notNull().default('default'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  balanceUsdCents: bigint('balance_usd_cents', { mode: 'bigint' }).notNull(),
  reservedUsdCents: bigint('reserved_usd_cents', { mode: 'bigint' }).notNull(),
  quotaTokensRemaining: bigint('quota_tokens_remaining', { mode: 'bigint' }),
  quotaMonthlyAllowance: bigint('quota_monthly_allowance', { mode: 'bigint' }),
  quotaReservedTokens: bigint('quota_reserved_tokens', { mode: 'bigint' }).notNull(),
  quotaResetAt: timestamp('quota_reset_at', { withTimezone: true }),
});

export const apiKeys = appSchema.table(
  'api_keys',
  {
    id: uuid('id').primaryKey().defaultRandom(),
    customerId: uuid('customer_id')
      .notNull()
      .references(() => customers.id),
    hash: text('hash').notNull(),
    label: text('label'),
    createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
    lastUsedAt: timestamp('last_used_at', { withTimezone: true }),
    revokedAt: timestamp('revoked_at', { withTimezone: true }),
  },
  (t) => ({
    byHash: index('api_key_hash_idx').on(t.hash),
    byCustomer: index('api_key_customer_idx').on(t.customerId),
  }),
);

export const authTokens = appSchema.table(
  'auth_tokens',
  {
    id: uuid('id').primaryKey().defaultRandom(),
    customerId: uuid('customer_id')
      .notNull()
      .references(() => customers.id),
    kind: authTokenKind('kind').notNull().default('customer_ui'),
    hash: text('hash').notNull(),
    label: text('label'),
    createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
    lastUsedAt: timestamp('last_used_at', { withTimezone: true }),
    revokedAt: timestamp('revoked_at', { withTimezone: true }),
  },
  (t) => ({
    byHash: index('auth_token_hash_idx').on(t.hash),
    byCustomer: index('auth_token_customer_idx').on(t.customerId),
  }),
);

export const reservations = appSchema.table(
  'reservations',
  {
    id: uuid('id').primaryKey().defaultRandom(),
    customerId: uuid('customer_id')
      .notNull()
      .references(() => customers.id),
    workId: text('work_id').notNull().unique(),
    kind: reservationKind('kind').notNull(),
    amountUsdCents: bigint('amount_usd_cents', { mode: 'bigint' }),
    amountTokens: bigint('amount_tokens', { mode: 'bigint' }),
    committedUsdCents: bigint('committed_usd_cents', { mode: 'bigint' }),
    committedTokens: bigint('committed_tokens', { mode: 'bigint' }),
    refundedUsdCents: bigint('refunded_usd_cents', { mode: 'bigint' }),
    refundedTokens: bigint('refunded_tokens', { mode: 'bigint' }),
    capability: text('capability'),
    model: text('model'),
    state: reservationState('state').notNull().default('open'),
    createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
    resolvedAt: timestamp('resolved_at', { withTimezone: true }),
  },
  (t) => ({
    byCustomer: index('reservation_customer_idx').on(t.customerId),
  }),
);

export const topups = appSchema.table(
  'topups',
  {
    id: uuid('id').primaryKey().defaultRandom(),
    customerId: uuid('customer_id')
      .notNull()
      .references(() => customers.id),
    stripeSessionId: text('stripe_session_id').notNull().unique(),
    amountUsdCents: bigint('amount_usd_cents', { mode: 'bigint' }).notNull(),
    status: topupStatus('status').notNull().default('pending'),
    createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
    disputedAt: timestamp('disputed_at', { withTimezone: true }),
    refundedAt: timestamp('refunded_at', { withTimezone: true }),
  },
  (t) => ({
    byCustomer: index('topup_customer_idx').on(t.customerId),
  }),
);

export const stripeWebhookEvents = appSchema.table('stripe_webhook_events', {
  eventId: text('event_id').primaryKey(),
  type: text('type').notNull(),
  receivedAt: timestamp('received_at', { withTimezone: true }).notNull().defaultNow(),
  payload: text('payload').notNull(),
});

export const adminAuditEvents = appSchema.table(
  'admin_audit_events',
  {
    id: uuid('id').primaryKey().defaultRandom(),
    actor: text('actor').notNull(),
    action: text('action').notNull(),
    targetId: text('target_id'),
    payload: text('payload'),
    statusCode: integer('status_code').notNull(),
    occurredAt: timestamp('occurred_at', { withTimezone: true }).notNull().defaultNow(),
  },
  (t) => ({
    byActorTime: index('admin_audit_event_actor_time_idx').on(t.actor, t.occurredAt),
  }),
);

export const idempotencyState = pgEnum('idempotency_state', ['pending', 'completed']);
export const idempotencyEncoding = pgEnum('idempotency_encoding', ['utf8', 'base64']);

export const idempotencyRequests = appSchema.table(
  'idempotency_requests',
  {
    id: uuid('id').primaryKey().defaultRandom(),
    customerId: uuid('customer_id')
      .notNull()
      .references(() => customers.id),
    idempotencyKey: text('idempotency_key').notNull(),
    requestMethod: text('request_method').notNull(),
    requestPath: text('request_path').notNull(),
    requestHash: text('request_hash').notNull(),
    state: idempotencyState('state').notNull().default('pending'),
    responseStatusCode: integer('response_status_code'),
    responseContentType: text('response_content_type'),
    responseEncoding: idempotencyEncoding('response_encoding'),
    responseBody: text('response_body'),
    createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
    completedAt: timestamp('completed_at', { withTimezone: true }),
  },
  (t) => ({
    byCustomerAndKey: uniqueIndex('idempotency_requests_customer_key_uniq').on(
      t.customerId,
      t.idempotencyKey,
    ),
    byCreatedAt: index('idempotency_requests_created_at_idx').on(t.createdAt),
  }),
);

export const schema = {
  customers,
  apiKeys,
  authTokens,
  reservations,
  topups,
  stripeWebhookEvents,
  adminAuditEvents,
  idempotencyRequests,
  customerTier,
  customerStatus,
  topupStatus,
  reservationState,
  reservationKind,
  authTokenKind,
  idempotencyState,
  idempotencyEncoding,
};
