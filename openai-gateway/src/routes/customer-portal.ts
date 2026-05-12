import { and, desc, eq, isNull, sql } from 'drizzle-orm';
import type { FastifyInstance, FastifyReply, FastifyRequest, preHandlerAsyncHookHandler } from 'fastify';
import { z } from 'zod';
import type { CustomerPortal } from '@livepeer-rewrite/customer-portal';
import { auth, db as portalDb } from '@livepeer-rewrite/customer-portal';
import type { Db } from '@livepeer-rewrite/customer-portal/db';
import type { RouteSelector } from '../service/routeSelector.js';
import { buildPortalModelCatalog } from '../service/catalog.js';

const SignupSchema = z.object({
  email: z.string().email(),
});

const LoginSchema = z.object({
  token: z.string().min(1),
  actor: z.string().trim().min(1),
});

const IssueKeySchema = z.object({
  label: z.string().min(1).max(128).optional(),
  env: z.enum(['live', 'test']).optional(),
});

const IssueAuthTokenSchema = z.object({
  label: z.string().min(1).max(128).optional(),
});

declare module 'fastify' {
  interface FastifyRequest {
    customerSession?: Awaited<ReturnType<CustomerPortal['customerTokenService']['authenticate']>>;
  }
}

export interface RegisterCustomerPortalRoutesDeps {
  db: Db;
  portal: CustomerPortal;
  authPepper: string;
  routeSelector: RouteSelector;
}

export function registerCustomerPortalRoutes(
  app: FastifyInstance,
  deps: RegisterCustomerPortalRoutesDeps,
): void {
  const requireCustomer = customerAuthPreHandler(deps.portal.customerTokenService);

  app.post('/portal/signup', async (req, reply) => {
    const parsed = SignupSchema.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: 'invalid_request', details: parsed.error.flatten() });
      return;
    }
    try {
      const customer = await deps.portal.adminEngine.createCustomer({
        email: parsed.data.email,
        actor: 'portal.signup',
      });
      const authToken = await deps.portal.customerTokenService.issue({
        customerId: customer.id,
        label: 'Primary UI token',
      });
      const apiKey = await deps.portal.issueApiKey({ customerId: customer.id, label: 'Primary key' });
      await reply.code(201).send({
        customer: serializeCustomer(customer),
        auth_token: authToken.plaintext,
        auth_token_id: authToken.tokenId,
        api_key: apiKey.plaintext,
        api_key_id: apiKey.apiKeyId,
      });
    } catch (err) {
      await reply.code(400).send({ error: 'signup_failed', message: errorMessage(err) });
    }
  });

  app.post('/portal/login', async (req, reply) => {
    const parsed = LoginSchema.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: 'invalid_request', details: parsed.error.flatten() });
      return;
    }
    try {
      const session = await deps.portal.customerTokenService.authenticate(`Bearer ${parsed.data.token}`);
      const keys = await deps.portal.customerTokenService.list(session.customer.id);
      await reply.code(200).send({
        actor: parsed.data.actor,
        customer: serializeCustomer(session.customer),
        auth_tokens: keys.map(serializeAuthToken),
      });
    } catch (err) {
      await reply.code(401).send({ error: 'authentication_failed', message: errorMessage(err) });
    }
  });

  app.get('/portal/account', { preHandler: requireCustomer }, async (req, reply) => {
    const session = req.customerSession!;
    await reply.code(200).send({ customer: serializeCustomer(session.customer) });
  });

  app.get('/portal/account/limits', { preHandler: requireCustomer }, async (req, reply) => {
    const session = req.customerSession!;
    await reply.code(200).send({
      limits: {
        quota_tokens_remaining: session.customer.quotaTokensRemaining?.toString() ?? null,
        quota_monthly_allowance: session.customer.quotaMonthlyAllowance?.toString() ?? null,
        quota_reserved_tokens: session.customer.quotaReservedTokens.toString(),
        quota_reset_at: session.customer.quotaResetAt?.toISOString() ?? null,
        balance_usd_cents: session.customer.balanceUsdCents.toString(),
        reserved_usd_cents: session.customer.reservedUsdCents.toString(),
      },
    });
  });

  app.get('/portal/auth-tokens', { preHandler: requireCustomer }, async (req, reply) => {
    const session = req.customerSession!;
    const tokens = await deps.portal.customerTokenService.list(session.customer.id);
    await reply.code(200).send({ auth_tokens: tokens.map(serializeAuthToken) });
  });

  app.post('/portal/auth-tokens', { preHandler: requireCustomer }, async (req, reply) => {
    const session = req.customerSession!;
    const parsed = IssueAuthTokenSchema.safeParse(req.body ?? {});
    if (!parsed.success) {
      await reply.code(400).send({ error: 'invalid_request', details: parsed.error.flatten() });
      return;
    }
    const issued = await deps.portal.customerTokenService.issue({
      customerId: session.customer.id,
      ...(parsed.data.label !== undefined ? { label: parsed.data.label } : {}),
    });
    await reply.code(201).send({
      auth_token: issued.plaintext,
      auth_token_id: issued.tokenId,
    });
  });

  app.delete<{ Params: { id: string } }>('/portal/auth-tokens/:id', { preHandler: requireCustomer }, async (req, reply) => {
    const session = req.customerSession!;
    const tokens = await deps.portal.customerTokenService.list(session.customer.id);
    const token = tokens.find((row) => row.id === req.params.id);
    if (!token) {
      await reply.code(404).send({ error: 'not_found' });
      return;
    }
    const activeCount = await deps.portal.customerTokenService.countActive(session.customer.id);
    if (!token.revokedAt && activeCount <= 1) {
      await reply.code(409).send({ error: 'last_active_token', message: 'cannot revoke last active auth token' });
      return;
    }
    await deps.portal.customerTokenService.revoke(token.id);
    await reply.code(204).send();
  });

  app.get('/portal/api-keys', { preHandler: requireCustomer }, async (req, reply) => {
    const session = req.customerSession!;
    const keys = await deps.db
      .select()
      .from(portalDb.apiKeys)
      .where(eq(portalDb.apiKeys.customerId, session.customer.id))
      .orderBy(desc(portalDb.apiKeys.createdAt));
    await reply.code(200).send({ api_keys: keys.map(serializeApiKey) });
  });

  app.post('/portal/api-keys', { preHandler: requireCustomer }, async (req, reply) => {
    const session = req.customerSession!;
    const parsed = IssueKeySchema.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: 'invalid_request', details: parsed.error.flatten() });
      return;
    }
    const issued = await auth.issueKey(deps.db, {
      customerId: session.customer.id,
      envPrefix: parsed.data.env ?? 'live',
      pepper: deps.authPepper,
      ...(parsed.data.label !== undefined ? { label: parsed.data.label } : {}),
    });
    await reply.code(201).send({
      api_key: issued.plaintext,
      api_key_id: issued.apiKeyId,
    });
  });

  app.delete<{ Params: { id: string } }>('/portal/api-keys/:id', { preHandler: requireCustomer }, async (req, reply) => {
    const session = req.customerSession!;
    const rows = await deps.db
      .select()
      .from(portalDb.apiKeys)
      .where(eq(portalDb.apiKeys.id, req.params.id))
      .limit(1);
    const row = rows[0];
    if (!row || row.customerId !== session.customer.id) {
      await reply.code(404).send({ error: 'not_found' });
      return;
    }
    const activeCountRows = await deps.db
      .select({ count: sql<number>`count(*)::int` })
      .from(portalDb.apiKeys)
      .where(and(eq(portalDb.apiKeys.customerId, session.customer.id), isNull(portalDb.apiKeys.revokedAt)));
    const activeCount = activeCountRows[0]?.count ?? 0;
    if (!row.revokedAt && activeCount <= 1) {
      await reply.code(409).send({ error: 'last_active_key', message: 'cannot revoke last active API key' });
      return;
    }
    await auth.revokeKey(deps.db, row.id);
    await reply.code(204).send();
  });

  app.get('/portal/topups', { preHandler: requireCustomer }, async (req, reply) => {
    const session = req.customerSession!;
    const topups = await deps.db
      .select()
      .from(portalDb.topups)
      .where(eq(portalDb.topups.customerId, session.customer.id))
      .orderBy(desc(portalDb.topups.createdAt));
    await reply.code(200).send({
      topups: topups.map((row: typeof portalDb.topups.$inferSelect) => ({
        id: row.id,
        stripe_session_id: row.stripeSessionId,
        amount_usd_cents: row.amountUsdCents.toString(),
        status: row.status,
        created_at: row.createdAt.toISOString(),
        disputed_at: row.disputedAt?.toISOString() ?? null,
        refunded_at: row.refundedAt?.toISOString() ?? null,
      })),
    });
  });

  app.get('/portal/usage', { preHandler: requireCustomer }, async (req, reply) => {
    const session = req.customerSession!;
    const reservations = await deps.db
      .select()
      .from(portalDb.reservations)
      .where(eq(portalDb.reservations.customerId, session.customer.id))
      .orderBy(desc(portalDb.reservations.createdAt))
      .limit(100);
    const grouped = await deps.db.execute(sql`
      select
        date_trunc('day', created_at) as day,
        coalesce(model, 'unknown') as model,
        coalesce(capability, 'unknown') as capability,
        count(*)::int as reservations,
        coalesce(sum(amount_usd_cents), 0)::text as amount_usd_cents,
        coalesce(sum(committed_usd_cents), 0)::text as committed_usd_cents,
        coalesce(sum(amount_tokens), 0)::text as amount_tokens,
        coalesce(sum(committed_tokens), 0)::text as committed_tokens
      from app.reservations
      where customer_id = ${session.customer.id}::uuid
      group by 1, 2, 3
      order by 1 desc, 2 asc, 3 asc
      limit 365
    `);
    await reply.code(200).send({
      grouped: grouped.rows.map((row) => ({
        day: row['day'] instanceof Date ? row['day'].toISOString() : String(row['day']),
        model: String(row['model']),
        capability: String(row['capability']),
        reservations: Number(row['reservations']),
        amount_usd_cents: String(row['amount_usd_cents']),
        committed_usd_cents: String(row['committed_usd_cents']),
        amount_tokens: String(row['amount_tokens']),
        committed_tokens: String(row['committed_tokens']),
      })),
      reservations: reservations.map(serializeReservation),
    });
  });

  app.get('/portal/playground/catalog', { preHandler: requireCustomer }, async (_req, reply) => {
    const catalog = buildPortalModelCatalog(await deps.routeSelector.inspect());
    await reply.code(200).send(catalog);
  });

  app.get<{ Params: { id: string } }>('/portal/usage/:id', { preHandler: requireCustomer }, async (req, reply) => {
    const session = req.customerSession!;
    const rows = await deps.db
      .select()
      .from(portalDb.reservations)
      .where(eq(portalDb.reservations.id, req.params.id))
      .limit(1);
    const row = rows[0];
    if (!row || row.customerId !== session.customer.id) {
      await reply.code(404).send({ error: 'not_found' });
      return;
    }
    await reply.code(200).send({ reservation: serializeReservation(row) });
  });
}

function customerAuthPreHandler(
  service: CustomerPortal['customerTokenService'],
): preHandlerAsyncHookHandler {
  return async (req: FastifyRequest, reply: FastifyReply): Promise<void> => {
    try {
      req.customerSession = await service.authenticate(req.headers.authorization);
    } catch (err) {
      await reply.code(401).send({ error: 'authentication_failed', message: errorMessage(err) });
    }
  };
}

function serializeCustomer(customer: {
  id: string;
  email: string;
  tier: string;
  status: string;
  balanceUsdCents: bigint;
  reservedUsdCents: bigint;
}): Record<string, unknown> {
  return {
    id: customer.id,
    email: customer.email,
    tier: customer.tier,
    status: customer.status,
    balance_usd_cents: customer.balanceUsdCents.toString(),
    reserved_usd_cents: customer.reservedUsdCents.toString(),
  };
}

function serializeApiKey(row: typeof portalDb.apiKeys.$inferSelect): Record<string, unknown> {
  return {
    id: row.id,
    label: row.label,
    created_at: row.createdAt.toISOString(),
    last_used_at: row.lastUsedAt?.toISOString() ?? null,
    revoked_at: row.revokedAt?.toISOString() ?? null,
  };
}

function serializeAuthToken(row: typeof portalDb.authTokens.$inferSelect): Record<string, unknown> {
  return {
    id: row.id,
    label: row.label,
    created_at: row.createdAt.toISOString(),
    last_used_at: row.lastUsedAt?.toISOString() ?? null,
    revoked_at: row.revokedAt?.toISOString() ?? null,
  };
}

function serializeReservation(row: typeof portalDb.reservations.$inferSelect): Record<string, unknown> {
  return {
    id: row.id,
    work_id: row.workId,
    kind: row.kind,
    state: row.state,
    capability: row.capability ?? null,
    model: row.model ?? null,
    amount_usd_cents: row.amountUsdCents?.toString() ?? null,
    committed_usd_cents: row.committedUsdCents?.toString() ?? null,
    refunded_usd_cents: row.refundedUsdCents?.toString() ?? null,
    amount_tokens: row.amountTokens?.toString() ?? null,
    committed_tokens: row.committedTokens?.toString() ?? null,
    refunded_tokens: row.refundedTokens?.toString() ?? null,
    created_at: row.createdAt.toISOString(),
    resolved_at: row.resolvedAt?.toISOString() ?? null,
  };
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
