import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createInMemoryWallet } from '../src/testing/index.js';
import type { CostQuote, UsageReport } from '../src/billing/index.js';

function quote(workId: string, cents: bigint): CostQuote {
  return {
    workId,
    cents,
    estimatedTokens: 0,
    model: 'm',
    capability: 'c',
    callerTier: 'prepaid',
  };
}

function usage(cents: bigint): UsageReport {
  return { cents, actualTokens: 0, model: 'm', capability: 'c' };
}

test('reserve deducts from balance; commit settles actual cost', async () => {
  const w = createInMemoryWallet({ initialBalanceCents: 1000n });
  const handle = await w.reserve('cust-1', quote('w-1', 200n));
  assert.ok(handle);
  assert.equal(w.balance('cust-1'), 800n);
  await w.commit(handle, usage(150n));
  assert.equal(w.balance('cust-1'), 850n);
});

test('refund returns reserved cents to balance', async () => {
  const w = createInMemoryWallet({ initialBalanceCents: 1000n });
  const handle = await w.reserve('cust-1', quote('w-1', 300n));
  assert.ok(handle);
  await w.refund(handle!);
  assert.equal(w.balance('cust-1'), 1000n);
});

test('reserve refuses to exceed balance', async () => {
  const w = createInMemoryWallet({ initialBalanceCents: 100n });
  await assert.rejects(() => w.reserve('cust-1', quote('w-1', 200n)));
});

test('reserve is idempotent-protected by workId', async () => {
  const w = createInMemoryWallet({ initialBalanceCents: 1000n });
  await w.reserve('cust-1', quote('w-1', 100n));
  await assert.rejects(() => w.reserve('cust-1', quote('w-1', 100n)));
});

test('commit twice on same handle throws ReservationNotOpenError', async () => {
  const w = createInMemoryWallet({ initialBalanceCents: 1000n });
  const handle = await w.reserve('cust-1', quote('w-1', 100n));
  await w.commit(handle, usage(100n));
  await assert.rejects(() => w.commit(handle, usage(100n)));
});

test('refund twice on same handle throws ReservationNotOpenError', async () => {
  const w = createInMemoryWallet({ initialBalanceCents: 1000n });
  const handle = await w.reserve('cust-1', quote('w-1', 100n));
  await w.refund(handle!);
  await assert.rejects(() => w.refund(handle!));
});

test('commit caps actual at reserved (no overshoot)', async () => {
  const w = createInMemoryWallet({ initialBalanceCents: 1000n });
  const handle = await w.reserve('cust-1', quote('w-1', 100n));
  await w.commit(handle, usage(500n));
  assert.equal(w.balance('cust-1'), 900n);
});
