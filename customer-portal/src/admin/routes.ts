import type { FastifyInstance, FastifyReply, FastifyRequest, preHandlerAsyncHookHandler } from 'fastify';
import { z } from 'zod';
import type { AdminAuthResolver } from '../auth/types.js';
import type { AdminEngine } from './engine.js';
import { toHttpError } from '../middleware/errors.js';

const CreateCustomerSchema = z.object({
  email: z.string().email(),
  tier: z.enum(['free', 'prepaid']).optional(),
  rate_limit_tier: z.string().optional(),
  initial_balance_usd_cents: z.union([z.number(), z.string()]).optional(),
});

const AdjustBalanceSchema = z.object({
  delta_usd_cents: z.union([z.number(), z.string()]),
  reason: z.string().min(1),
});

const RefundSchema = z.object({
  stripe_session_id: z.string().min(1),
  reason: z.string().min(1),
});

const SetStatusSchema = z.object({
  status: z.enum(['active', 'suspended', 'closed']),
});

declare module 'fastify' {
  interface FastifyRequest {
    adminActor?: string;
  }
}

export interface RegisterAdminRoutesDeps {
  engine: AdminEngine;
  authResolver: AdminAuthResolver;
  realm?: string;
}

function basicAuthPreHandler(
  resolver: AdminAuthResolver,
  realm: string,
): preHandlerAsyncHookHandler {
  return async (req: FastifyRequest, reply: FastifyReply): Promise<void> => {
    const result = await resolver.resolve({
      headers: req.headers as Record<string, string | undefined>,
      ip: req.ip,
    });
    if (!result) {
      reply.header('www-authenticate', `Basic realm="${realm}"`);
      await reply.code(401).send({
        error: { code: 'authentication_failed', message: 'admin authentication required', type: 'AdminAuthError' },
      });
      return;
    }
    req.adminActor = result.actor;
  };
}

export function registerAdminRoutes(app: FastifyInstance, deps: RegisterAdminRoutesDeps): void {
  const realm = deps.realm ?? 'customer-portal-admin';
  const preHandler = basicAuthPreHandler(deps.authResolver, realm);

  app.get('/admin/customers', { preHandler }, async (req, reply) => {
    const q = req.query as Record<string, string | undefined>;
    const limit = q.limit ? Math.min(Number(q.limit), 200) : 50;
    const customers = await deps.engine.searchCustomers({
      limit,
      ...(q.q !== undefined ? { q: q.q } : {}),
      ...(q.tier === 'free' || q.tier === 'prepaid' ? { tier: q.tier } : {}),
      ...(q.status === 'active' || q.status === 'suspended' || q.status === 'closed'
        ? { status: q.status }
        : {}),
    });
    await reply.code(200).send({ customers: customers.map(serializeCustomer) });
  });

  app.post('/admin/customers', { preHandler }, async (req, reply) => {
    const parsed = CreateCustomerSchema.safeParse(req.body);
    if (!parsed.success) {
      const { status, envelope } = toHttpError(parsed.error);
      await reply.code(status).send(envelope);
      return;
    }
    try {
      const initial =
        parsed.data.initial_balance_usd_cents !== undefined
          ? BigInt(parsed.data.initial_balance_usd_cents)
          : undefined;
      const customer = await deps.engine.createCustomer({
        email: parsed.data.email,
        ...(parsed.data.tier !== undefined ? { tier: parsed.data.tier } : {}),
        ...(parsed.data.rate_limit_tier !== undefined
          ? { rateLimitTier: parsed.data.rate_limit_tier }
          : {}),
        ...(initial !== undefined ? { initialBalanceUsdCents: initial } : {}),
        actor: req.adminActor!,
      });
      await reply.code(201).send({ customer: serializeCustomer(customer) });
    } catch (err) {
      const { status, envelope } = toHttpError(err);
      await reply.code(status).send(envelope);
    }
  });

  app.get<{ Params: { id: string } }>('/admin/customers/:id', { preHandler }, async (req, reply) => {
    const customer = await deps.engine.getCustomer(req.params.id);
    if (!customer) {
      await reply.code(404).send({ error: { code: 'not_found', message: 'customer not found', type: 'NotFound' } });
      return;
    }
    await reply.code(200).send({ customer: serializeCustomer(customer) });
  });

  app.get<{ Params: { id: string } }>('/admin/reservations/:id', { preHandler }, async (req, reply) => {
    const reservation = await deps.engine.getReservation(req.params.id);
    if (!reservation) {
      await reply.code(404).send({ error: { code: 'not_found', message: 'reservation not found', type: 'NotFound' } });
      return;
    }
    await reply.code(200).send({ reservation: serializeReservation(reservation) });
  });

  app.post<{ Params: { id: string } }>(
    '/admin/customers/:id/balance',
    { preHandler },
    async (req, reply) => {
      const parsed = AdjustBalanceSchema.safeParse(req.body);
      if (!parsed.success) {
        const { status, envelope } = toHttpError(parsed.error);
        await reply.code(status).send(envelope);
        return;
      }
      try {
        const customer = await deps.engine.adjustBalance({
          customerId: req.params.id,
          deltaUsdCents: BigInt(parsed.data.delta_usd_cents),
          reason: parsed.data.reason,
          actor: req.adminActor!,
        });
        await reply.code(200).send({ customer: serializeCustomer(customer) });
      } catch (err) {
        const { status, envelope } = toHttpError(err);
        await reply.code(status).send(envelope);
      }
    },
  );

  app.post<{ Params: { id: string } }>(
    '/admin/customers/:id/status',
    { preHandler },
    async (req, reply) => {
      const parsed = SetStatusSchema.safeParse(req.body);
      if (!parsed.success) {
        const { status, envelope } = toHttpError(parsed.error);
        await reply.code(status).send(envelope);
        return;
      }
      const ok = await deps.engine.setStatus({
        customerId: req.params.id,
        status: parsed.data.status,
        actor: req.adminActor!,
      });
      if (!ok) {
        await reply.code(404).send({ error: { code: 'not_found', message: 'customer not found', type: 'NotFound' } });
        return;
      }
      await reply.code(200).send({ ok: true });
    },
  );

  app.post<{ Params: { id: string } }>(
    '/admin/customers/:id/refund',
    { preHandler },
    async (req, reply) => {
      const parsed = RefundSchema.safeParse(req.body);
      if (!parsed.success) {
        const { status, envelope } = toHttpError(parsed.error);
        await reply.code(status).send(envelope);
        return;
      }
      try {
        const result = await deps.engine.refundTopup({
          stripeSessionId: parsed.data.stripe_session_id,
          reason: parsed.data.reason,
          actor: req.adminActor!,
        });
        await reply.code(200).send(result);
      } catch (err) {
        const { status, envelope } = toHttpError(err);
        await reply.code(status).send(envelope);
      }
    },
  );

  app.get('/admin/audit', { preHandler }, async (req, reply) => {
    const q = req.query as Record<string, string | undefined>;
    const limit = q.limit ? Math.min(Number(q.limit), 200) : 50;
    const events = await deps.engine.listAudit({
      limit,
      ...(q.actor !== undefined ? { actor: q.actor } : {}),
      ...(q.action !== undefined ? { action: q.action } : {}),
    });
    await reply.code(200).send({ events });
  });

  app.get('/admin/topups', { preHandler }, async (req, reply) => {
    const q = req.query as Record<string, string | undefined>;
    const limit = q.limit ? Math.min(Number(q.limit), 200) : 50;
    const topups = await deps.engine.listTopups({
      limit,
      ...(q.customer_id !== undefined ? { customerId: q.customer_id } : {}),
      ...(q.status === 'pending' || q.status === 'succeeded' || q.status === 'failed' || q.status === 'refunded'
        ? { status: q.status }
        : {}),
    });
    await reply.code(200).send({
      topups: topups.map((row) => ({
        id: row.id,
        customer_id: row.customerId,
        stripe_session_id: row.stripeSessionId,
        amount_usd_cents: row.amountUsdCents.toString(),
        status: row.status,
        created_at: row.createdAt.toISOString(),
        disputed_at: row.disputedAt?.toISOString() ?? null,
        refunded_at: row.refundedAt?.toISOString() ?? null,
      })),
    });
  });

  app.get('/admin/reservations', { preHandler }, async (req, reply) => {
    const q = req.query as Record<string, string | undefined>;
    const limit = q.limit ? Math.min(Number(q.limit), 200) : 100;
    const reservations = await deps.engine.listReservations({
      limit,
      ...(q.customer_id !== undefined ? { customerId: q.customer_id } : {}),
    });
    await reply.code(200).send({
      reservations: reservations.map((row) => ({
        id: row.id,
        customer_id: row.customerId,
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
      })),
    });
  });
}

function serializeCustomer(c: { id: string; email: string; tier: string; status: string; balanceUsdCents: bigint; reservedUsdCents: bigint }): Record<string, unknown> {
  return {
    id: c.id,
    email: c.email,
    tier: c.tier,
    status: c.status,
    balance_usd_cents: c.balanceUsdCents.toString(),
    reserved_usd_cents: c.reservedUsdCents.toString(),
  };
}

function serializeReservation(row: {
  id: string;
  customerId: string;
  workId: string;
  kind: string;
  state: string;
  capability: string | null;
  model: string | null;
  amountUsdCents: bigint | null;
  committedUsdCents: bigint | null;
  refundedUsdCents: bigint | null;
  amountTokens: bigint | null;
  committedTokens: bigint | null;
  refundedTokens: bigint | null;
  createdAt: Date;
  resolvedAt: Date | null;
}): Record<string, unknown> {
  return {
    id: row.id,
    customer_id: row.customerId,
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
