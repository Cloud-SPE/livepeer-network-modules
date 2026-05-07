import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  createTopupCheckoutSession,
  InvalidTopupAmountError,
  handleStripeWebhook,
} from '../src/billing/stripe/index.js';
import {
  createMockStripeClient,
  createInMemoryWebhookEventStore,
} from '../src/testing/index.js';

test('createTopupCheckoutSession rejects amount below min', async () => {
  const stripe = createMockStripeClient();
  await assert.rejects(
    () =>
      createTopupCheckoutSession(
        stripe,
        { priceMinCents: 500, priceMaxCents: 100_000 },
        {
          customerId: 'cust-1',
          amountUsdCents: 100,
          successUrl: 'https://x',
          cancelUrl: 'https://y',
        },
      ),
    InvalidTopupAmountError,
  );
});

test('createTopupCheckoutSession rejects amount above max', async () => {
  const stripe = createMockStripeClient();
  await assert.rejects(
    () =>
      createTopupCheckoutSession(
        stripe,
        { priceMinCents: 500, priceMaxCents: 100_000 },
        {
          customerId: 'cust-1',
          amountUsdCents: 200_000,
          successUrl: 'https://x',
          cancelUrl: 'https://y',
        },
      ),
    InvalidTopupAmountError,
  );
});

test('createTopupCheckoutSession returns session url for valid amount', async () => {
  const stripe = createMockStripeClient();
  const result = await createTopupCheckoutSession(
    stripe,
    { priceMinCents: 500, priceMaxCents: 100_000 },
    {
      customerId: 'cust-1',
      amountUsdCents: 5_000,
      successUrl: 'https://x',
      cancelUrl: 'https://y',
    },
  );
  assert.match(result.sessionId, /^cs_test_/);
  assert.match(result.url, /^https:\/\//);
  assert.equal(stripe.checkoutCalls.length, 1);
  assert.equal(stripe.checkoutCalls[0]!.customerId, 'cust-1');
});

test('handleStripeWebhook returns signature_invalid when signature header missing', async () => {
  const stripe = createMockStripeClient();
  const store = createInMemoryWebhookEventStore();
  const result = await handleStripeWebhook(
    { stripe, store },
    { rawBody: '{}', signature: undefined },
  );
  assert.equal(result.outcome, 'signature_invalid');
});

test('handleStripeWebhook returns signature_invalid when constructEvent throws', async () => {
  const stripe = createMockStripeClient();
  const store = createInMemoryWebhookEventStore();
  const result = await handleStripeWebhook(
    { stripe, store },
    { rawBody: '{}', signature: 'bogus-sig' },
  );
  assert.equal(result.outcome, 'signature_invalid');
});

test('handleStripeWebhook processes checkout.session.completed and credits topup', async () => {
  const stripe = createMockStripeClient();
  stripe.registerEvent('valid-sig', {
    id: 'evt_1',
    type: 'checkout.session.completed',
    data: {
      object: {
        id: 'cs_1',
        client_reference_id: 'cust-1',
        amount_total: 1000,
      },
    },
  });
  const store = createInMemoryWebhookEventStore();
  const result = await handleStripeWebhook(
    { stripe, store },
    { rawBody: '{}', signature: 'valid-sig' },
  );
  assert.equal(result.outcome, 'processed');
  assert.equal(result.eventType, 'checkout.session.completed');
  assert.equal(store.topupCredits.length, 1);
  assert.equal(store.topupCredits[0]!.customerId, 'cust-1');
  assert.equal(store.topupCredits[0]!.amountUsdCents, 1000n);
});

test('handleStripeWebhook is idempotent on event_id replay', async () => {
  const stripe = createMockStripeClient();
  stripe.registerEvent('sig-replay', {
    id: 'evt_replay',
    type: 'checkout.session.completed',
    data: {
      object: { id: 'cs_replay', client_reference_id: 'cust-1', amount_total: 500 },
    },
  });
  const store = createInMemoryWebhookEventStore();

  const r1 = await handleStripeWebhook({ stripe, store }, { rawBody: '{}', signature: 'sig-replay' });
  assert.equal(r1.outcome, 'processed');

  const r2 = await handleStripeWebhook({ stripe, store }, { rawBody: '{}', signature: 'sig-replay' });
  assert.equal(r2.outcome, 'duplicate');

  assert.equal(store.topupCredits.length, 1);
});

test('handleStripeWebhook returns unsupported for unrecognized event types', async () => {
  const stripe = createMockStripeClient();
  stripe.registerEvent('sig-unknown', {
    id: 'evt_unknown',
    type: 'invoice.paid',
    data: { object: {} },
  });
  const store = createInMemoryWebhookEventStore();
  const result = await handleStripeWebhook(
    { stripe, store },
    { rawBody: '{}', signature: 'sig-unknown' },
  );
  assert.equal(result.outcome, 'unsupported');
});

test('handleStripeWebhook handles charge.dispute.created with metadata', async () => {
  const stripe = createMockStripeClient();
  stripe.registerEvent('sig-dispute', {
    id: 'evt_dispute',
    type: 'charge.dispute.created',
    data: { object: { metadata: { stripe_session_id: 'cs_disputed' } } },
  });
  const store = createInMemoryWebhookEventStore();
  const result = await handleStripeWebhook(
    { stripe, store },
    { rawBody: '{}', signature: 'sig-dispute' },
  );
  assert.equal(result.outcome, 'processed');
  assert.equal(store.disputes[0], 'cs_disputed');
});

test('handleStripeWebhook returns handler_error when checkout payload incomplete', async () => {
  const stripe = createMockStripeClient();
  stripe.registerEvent('sig-bad', {
    id: 'evt_bad',
    type: 'checkout.session.completed',
    data: { object: { id: 'cs_x' } },
  });
  const store = createInMemoryWebhookEventStore();
  const result = await handleStripeWebhook(
    { stripe, store },
    { rawBody: '{}', signature: 'sig-bad' },
  );
  assert.equal(result.outcome, 'handler_error');
});
