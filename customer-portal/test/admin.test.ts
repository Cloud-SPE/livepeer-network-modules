import { test } from 'node:test';
import assert from 'node:assert/strict';
import Fastify from 'fastify';
import { registerAdminRoutes } from '../src/admin/index.js';
import { createBasicAdminAuthResolver } from '../src/auth/index.js';
import type { AdminEngine } from '../src/admin/index.js';

function fakeEngine(): AdminEngine & {
  customers: Map<string, {
    id: string;
    email: string;
    tier: string;
    status: 'active' | 'suspended' | 'closed';
    balanceUsdCents: bigint;
    reservedUsdCents: bigint;
  }>;
  audit: Array<{ actor: string; action: string }>;
  reservations: Array<{
    id: string;
    customerId: string;
    workId: string;
    kind: 'prepaid' | 'free';
    state: 'open' | 'committed' | 'refunded';
    amountUsdCents: bigint | null;
    amountTokens: bigint | null;
    committedUsdCents: bigint | null;
    committedTokens: bigint | null;
    refundedUsdCents: bigint | null;
    refundedTokens: bigint | null;
    capability: string | null;
    model: string | null;
    createdAt: Date;
    resolvedAt: Date | null;
  }>;
} {
  const customers = new Map<string, {
    id: string;
    email: string;
    tier: string;
    status: 'active' | 'suspended' | 'closed';
    balanceUsdCents: bigint;
    reservedUsdCents: bigint;
  }>();
  const audit: Array<{ actor: string; action: string }> = [];
  const reservations: Array<{
    id: string;
    customerId: string;
    workId: string;
    kind: 'prepaid' | 'free';
    state: 'open' | 'committed' | 'refunded';
    amountUsdCents: bigint | null;
    amountTokens: bigint | null;
    committedUsdCents: bigint | null;
    committedTokens: bigint | null;
    refundedUsdCents: bigint | null;
    refundedTokens: bigint | null;
    capability: string | null;
    model: string | null;
    createdAt: Date;
    resolvedAt: Date | null;
  }> = [];
  let n = 0;

  return {
    customers,
    audit,
    reservations,
    async createCustomer(input) {
      n += 1;
      const c = {
        id: `cust-${n}`,
        email: input.email,
        tier: input.tier ?? 'free',
        status: 'active' as const,
        balanceUsdCents: input.initialBalanceUsdCents ?? 0n,
        reservedUsdCents: 0n,
      };
      customers.set(c.id, c);
      audit.push({ actor: input.actor, action: 'customer.create' });
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      return c as any;
    },
    async getCustomer(id) {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      return (customers.get(id) ?? null) as any;
    },
    async searchCustomers() {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      return Array.from(customers.values()) as any[];
    },
    async listTopups() {
      return [];
    },
    async listReservations(input) {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      return reservations.filter((row) => !input.customerId || row.customerId === input.customerId) as any[];
    },
    async adjustBalance(input) {
      const c = customers.get(input.customerId);
      if (!c) throw new Error('not found');
      c.balanceUsdCents += input.deltaUsdCents;
      audit.push({ actor: input.actor, action: 'customer.balance.adjust' });
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      return c as any;
    },
    async setStatus(input) {
      const c = customers.get(input.customerId);
      if (!c) return false;
      c.status = input.status;
      audit.push({ actor: input.actor, action: `customer.status.${input.status}` });
      return true;
    },
    async refundTopup(input) {
      audit.push({ actor: input.actor, action: 'topup.refund' });
      return {
        customerId: 'cust-1',
        amountReversedCents: '500',
        newBalanceUsdCents: '500',
        alreadyRefunded: false,
      };
    },
    async listAudit() {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      return audit.map((a, i) => ({
        id: `evt-${i}`,
        actor: a.actor,
        action: a.action,
        targetId: null,
        payload: null,
        statusCode: 200,
        occurredAt: new Date(),
      })) as any[];
    },
  };
}

function basicAuth(user: string, pass: string): string {
  return 'Basic ' + Buffer.from(`${user}:${pass}`).toString('base64');
}

test('admin routes 401 without basic auth', async () => {
  const engine = fakeEngine();
  const authResolver = createBasicAdminAuthResolver({ user: 'op', pass: 'sekrit' });
  const app = Fastify();
  registerAdminRoutes(app, { engine, authResolver });

  const res = await app.inject({ method: 'GET', url: '/admin/customers/cust-1' });
  assert.equal(res.statusCode, 401);
  assert.match(String(res.headers['www-authenticate']), /Basic realm/);
  await app.close();
});

test('admin routes round-trip: create, lookup, adjust balance, status, refund, audit', async () => {
  const engine = fakeEngine();
  const authResolver = createBasicAdminAuthResolver({ user: 'op', pass: 'sekrit' });
  const app = Fastify();
  registerAdminRoutes(app, { engine, authResolver });
  const auth = basicAuth('op', 'sekrit');

  const create = await app.inject({
    method: 'POST',
    url: '/admin/customers',
    headers: { authorization: auth, 'content-type': 'application/json' },
    payload: JSON.stringify({ email: 'alice@example.com', tier: 'prepaid' }),
  });
  assert.equal(create.statusCode, 201);
  const id = JSON.parse(create.body).customer.id;
  engine.reservations.push({
    id: 'res-1',
    customerId: id,
    workId: 'work-1',
    kind: 'prepaid',
    state: 'committed',
    amountUsdCents: 150n,
    amountTokens: null,
    committedUsdCents: 125n,
    committedTokens: null,
    refundedUsdCents: 25n,
    refundedTokens: null,
    capability: 'openai:/v1/chat/completions',
    model: 'gpt-4o-mini',
    createdAt: new Date('2026-05-08T12:00:00Z'),
    resolvedAt: new Date('2026-05-08T12:00:05Z'),
  });

  const get = await app.inject({
    method: 'GET',
    url: `/admin/customers/${id}`,
    headers: { authorization: auth },
  });
  assert.equal(get.statusCode, 200);

  const adj = await app.inject({
    method: 'POST',
    url: `/admin/customers/${id}/balance`,
    headers: { authorization: auth, 'content-type': 'application/json' },
    payload: JSON.stringify({ delta_usd_cents: 1000, reason: 'manual credit' }),
  });
  assert.equal(adj.statusCode, 200);
  assert.equal(JSON.parse(adj.body).customer.balance_usd_cents, '1000');

  const status = await app.inject({
    method: 'POST',
    url: `/admin/customers/${id}/status`,
    headers: { authorization: auth, 'content-type': 'application/json' },
    payload: JSON.stringify({ status: 'suspended' }),
  });
  assert.equal(status.statusCode, 200);

  const refund = await app.inject({
    method: 'POST',
    url: `/admin/customers/${id}/refund`,
    headers: { authorization: auth, 'content-type': 'application/json' },
    payload: JSON.stringify({ stripe_session_id: 'cs_x', reason: 'dup' }),
  });
  assert.equal(refund.statusCode, 200);

  const audit = await app.inject({
    method: 'GET',
    url: '/admin/audit',
    headers: { authorization: auth },
  });
  assert.equal(audit.statusCode, 200);
  const events = JSON.parse(audit.body).events;
  assert.ok(events.length >= 4);

  const reservations = await app.inject({
    method: 'GET',
    url: `/admin/reservations?customer_id=${encodeURIComponent(id)}`,
    headers: { authorization: auth },
  });
  assert.equal(reservations.statusCode, 200);
  const body = JSON.parse(reservations.body);
  assert.equal(body.reservations.length, 1);
  assert.equal(body.reservations[0].work_id, 'work-1');
});

test('admin routes reject invalid create payload', async () => {
  const engine = fakeEngine();
  const authResolver = createBasicAdminAuthResolver({ user: 'op', pass: 'sekrit' });
  const app = Fastify();
  registerAdminRoutes(app, { engine, authResolver });
  const auth = basicAuth('op', 'sekrit');

  const res = await app.inject({
    method: 'POST',
    url: '/admin/customers',
    headers: { authorization: auth, 'content-type': 'application/json' },
    payload: JSON.stringify({ email: 'not-an-email' }),
  });
  assert.equal(res.statusCode, 400);
  await app.close();
});

test('admin routes 404 on missing customer', async () => {
  const engine = fakeEngine();
  const authResolver = createBasicAdminAuthResolver({ user: 'op', pass: 'sekrit' });
  const app = Fastify();
  registerAdminRoutes(app, { engine, authResolver });
  const auth = basicAuth('op', 'sekrit');

  const res = await app.inject({
    method: 'GET',
    url: '/admin/customers/nonexistent',
    headers: { authorization: auth },
  });
  assert.equal(res.statusCode, 404);
  await app.close();
});
