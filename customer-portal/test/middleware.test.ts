import { test } from 'node:test';
import assert from 'node:assert/strict';
import Fastify from 'fastify';
import {
  authPreHandler,
  rateLimitPreHandler,
  walletReservePreHandler,
  toHttpError,
  RateLimitExceededError,
  type RateLimiter,
} from '../src/middleware/index.js';
import { InvalidApiKeyError, AccountSuspendedError } from '../src/auth/index.js';
import { BalanceInsufficientError } from '../src/billing/index.js';
import type { AuthResolver, Caller } from '../src/auth/types.js';
import type { Wallet } from '../src/billing/types.js';

function fakeAuthResolver(caller: Caller | null): AuthResolver {
  return {
    async resolve() {
      return caller;
    },
  };
}

function fakeRateLimiter(): RateLimiter & { calls: number } {
  let calls = 0;
  return {
    get calls() {
      return calls;
    },
    async check() {
      calls += 1;
      return {
        headers: { limitRequests: 10, remainingRequests: 9, resetSeconds: 60 },
        concurrencyKey: 'k1',
        failedOpen: false,
      };
    },
    async release() {
      // noop
    },
  };
}

function trackingWallet(): Wallet & { reserveCalls: number } {
  let reserveCalls = 0;
  return {
    get reserveCalls() {
      return reserveCalls;
    },
    async reserve(_callerId, _quote) {
      reserveCalls += 1;
      return { reservationId: 'r1' };
    },
    async commit() {},
    async refund() {},
  };
}

test('middleware composition fires auth → rate-limit → wallet → handler in order', async () => {
  const order: string[] = [];
  const caller: Caller = { id: 'cust-1', tier: 'prepaid', rateLimitTier: 'default' };
  const auth = fakeAuthResolver(caller);
  const limiter: RateLimiter = {
    async check() {
      order.push('rate-limit');
      return {
        headers: { limitRequests: 10, remainingRequests: 9, resetSeconds: 60 },
        concurrencyKey: 'k1',
        failedOpen: false,
      };
    },
    async release() {},
  };
  const wallet: Wallet = {
    async reserve() {
      order.push('wallet');
      return { reservationId: 'r1' };
    },
    async commit() {},
    async refund() {},
  };

  const app = Fastify();
  app.post(
    '/run',
    {
      preHandler: [
        async (req) => {
          order.push('pre-auth');
          const c = await auth.resolve({ headers: req.headers as Record<string, string | undefined>, ip: req.ip });
          if (!c) throw new Error('no caller');
          req.caller = c;
        },
        rateLimitPreHandler(limiter),
        walletReservePreHandler({
          wallet,
          quote: () => ({
            workId: 'w1',
            cents: 10n,
            estimatedTokens: 0,
            model: 'm',
            capability: 'c',
            callerTier: 'prepaid',
          }),
        }),
      ],
    },
    async () => {
      order.push('handler');
      return { ok: true };
    },
  );
  const res = await app.inject({ method: 'POST', url: '/run' });
  assert.equal(res.statusCode, 200);
  assert.deepEqual(order, ['pre-auth', 'rate-limit', 'wallet', 'handler']);
  await app.close();
});

test('authPreHandler 401s when resolver returns null', async () => {
  const app = Fastify();
  app.get('/protected', { preHandler: authPreHandler(fakeAuthResolver(null)) }, async () => ({ ok: true }));
  const res = await app.inject({ method: 'GET', url: '/protected' });
  assert.equal(res.statusCode, 401);
  await app.close();
});

test('authPreHandler attaches caller when resolver returns one', async () => {
  const caller: Caller = { id: 'c1', tier: 'free', rateLimitTier: 'default' };
  const app = Fastify();
  app.get('/me', { preHandler: authPreHandler(fakeAuthResolver(caller)) }, async (req) => ({
    id: req.caller?.id,
  }));
  const res = await app.inject({ method: 'GET', url: '/me' });
  assert.equal(res.statusCode, 200);
  assert.equal(JSON.parse(res.body).id, 'c1');
  await app.close();
});

test('rateLimitPreHandler short-circuits 429 on RateLimitExceededError', async () => {
  const caller: Caller = { id: 'c1', tier: 'prepaid', rateLimitTier: 'default' };
  const limiter: RateLimiter = {
    async check() {
      throw new RateLimitExceededError(60, 30);
    },
    async release() {},
  };
  const app = Fastify();
  app.get(
    '/rl',
    {
      preHandler: [
        async (req) => {
          req.caller = caller;
        },
        rateLimitPreHandler(limiter),
      ],
    },
    async () => ({ ok: true }),
  );
  const res = await app.inject({ method: 'GET', url: '/rl' });
  assert.equal(res.statusCode, 429);
  assert.equal(res.headers['retry-after'], '30');
  await app.close();
});

test('walletReservePreHandler 401s without a caller', async () => {
  const wallet = trackingWallet();
  const app = Fastify();
  app.post(
    '/reserve',
    {
      preHandler: walletReservePreHandler({
        wallet,
        quote: () => ({
          workId: 'w1',
          cents: 10n,
          estimatedTokens: 0,
          model: 'm',
          capability: 'c',
          callerTier: 'prepaid',
        }),
      }),
    },
    async () => ({ ok: true }),
  );
  const res = await app.inject({ method: 'POST', url: '/reserve' });
  assert.equal(res.statusCode, 401);
  assert.equal(wallet.reserveCalls, 0);
  await app.close();
});

test('rateLimitPreHandler skips silently when no caller (chains to next)', async () => {
  const limiter = fakeRateLimiter();
  const app = Fastify();
  app.get('/x', { preHandler: rateLimitPreHandler(limiter) }, async () => ({ ok: true }));
  const res = await app.inject({ method: 'GET', url: '/x' });
  assert.equal(res.statusCode, 200);
  assert.equal(limiter.calls, 0);
  await app.close();
});

test('toHttpError maps InvalidApiKeyError → 401', () => {
  const { status, envelope } = toHttpError(new InvalidApiKeyError());
  assert.equal(status, 401);
  assert.equal(envelope.error.code, 'authentication_failed');
});

test('toHttpError maps AccountSuspendedError → 403', () => {
  const { status, envelope } = toHttpError(new AccountSuspendedError('cust-1'));
  assert.equal(status, 403);
  assert.equal(envelope.error.code, 'account_inactive');
});

test('toHttpError maps RateLimitExceededError → 429', () => {
  const { status, envelope } = toHttpError(new RateLimitExceededError(60, 30));
  assert.equal(status, 429);
  assert.equal(envelope.error.code, 'rate_limit_exceeded');
});

test('toHttpError maps BalanceInsufficientError → 402', () => {
  const { status, envelope } = toHttpError(new BalanceInsufficientError(10n, 100n));
  assert.equal(status, 402);
  assert.equal(envelope.error.code, 'payment_required');
});

test('toHttpError maps unknown Error → 500', () => {
  const { status } = toHttpError(new Error('boom'));
  assert.equal(status, 500);
});

test('toHttpError maps non-Error values → 500', () => {
  const { status } = toHttpError('boom');
  assert.equal(status, 500);
});
