import { desc, eq } from 'drizzle-orm';
import type { FastifyInstance, FastifyReply, FastifyRequest, preHandlerAsyncHookHandler } from 'fastify';
import { z } from 'zod';
import type { CustomerPortal } from '@livepeer-rewrite/customer-portal';
import { auth, db as portalDb } from '@livepeer-rewrite/customer-portal';
import type { Db } from '@livepeer-rewrite/customer-portal/db';

const SignupSchema = z.object({
  email: z.string().email(),
});

const LoginSchema = z.object({
  api_key: z.string().min(1),
});

const IssueKeySchema = z.object({
  label: z.string().min(1).max(128).optional(),
  env: z.enum(['live', 'test']).optional(),
});

declare module 'fastify' {
  interface FastifyRequest {
    customerCaller?: Awaited<ReturnType<CustomerPortal['authService']['authenticate']>>;
  }
}

export interface RegisterCustomerPortalRoutesDeps {
  db: Db;
  portal: CustomerPortal;
  authPepper: string;
}

export function registerCustomerPortalRoutes(
  app: FastifyInstance,
  deps: RegisterCustomerPortalRoutesDeps,
): void {
  const requireCustomer = customerAuthPreHandler(deps.portal.authService);

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
      const apiKey = await auth.issueKey(deps.db, {
        customerId: customer.id,
        envPrefix: 'live',
        pepper: deps.authPepper,
        label: 'Primary key',
      });
      await reply.code(201).send({
        customer: serializeCustomer(customer),
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
      const caller = await deps.portal.authService.authenticate(`Bearer ${parsed.data.api_key}`);
      const keys = await deps.db
        .select()
        .from(portalDb.apiKeys)
        .where(eq(portalDb.apiKeys.customerId, caller.id))
        .orderBy(desc(portalDb.apiKeys.createdAt));
      await reply.code(200).send({
        customer: serializeCustomer(caller.customer),
        api_keys: keys.map(serializeApiKey),
      });
    } catch (err) {
      await reply.code(401).send({ error: 'authentication_failed', message: errorMessage(err) });
    }
  });

  app.get('/portal/account', { preHandler: requireCustomer }, async (req, reply) => {
    const caller = req.customerCaller!;
    await reply.code(200).send({ customer: serializeCustomer(caller.customer) });
  });

  app.get('/portal/api-keys', { preHandler: requireCustomer }, async (req, reply) => {
    const caller = req.customerCaller!;
    const keys = await deps.db
      .select()
      .from(portalDb.apiKeys)
      .where(eq(portalDb.apiKeys.customerId, caller.id))
      .orderBy(desc(portalDb.apiKeys.createdAt));
    await reply.code(200).send({ api_keys: keys.map(serializeApiKey) });
  });

  app.post('/portal/api-keys', { preHandler: requireCustomer }, async (req, reply) => {
    const caller = req.customerCaller!;
    const parsed = IssueKeySchema.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: 'invalid_request', details: parsed.error.flatten() });
      return;
    }
    const issued = await auth.issueKey(deps.db, {
      customerId: caller.id,
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
    const caller = req.customerCaller!;
    const rows = await deps.db
      .select()
      .from(portalDb.apiKeys)
      .where(eq(portalDb.apiKeys.id, req.params.id))
      .limit(1);
    const row = rows[0];
    if (!row || row.customerId !== caller.id) {
      await reply.code(404).send({ error: 'not_found' });
      return;
    }
    await auth.revokeKey(deps.db, row.id);
    await reply.code(204).send();
  });

  app.get('/portal/topups', { preHandler: requireCustomer }, async (req, reply) => {
    const caller = req.customerCaller!;
    const topups = await deps.db
      .select()
      .from(portalDb.topups)
      .where(eq(portalDb.topups.customerId, caller.id))
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
}

function customerAuthPreHandler(
  service: CustomerPortal['authService'],
): preHandlerAsyncHookHandler {
  return async (req: FastifyRequest, reply: FastifyReply): Promise<void> => {
    try {
      req.customerCaller = await service.authenticate(req.headers.authorization);
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

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
