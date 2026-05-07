import test from "node:test";
import assert from "node:assert/strict";

import { ConfigSchema } from "../../src/config.js";
import { buildServer } from "../../src/server.js";
import type {
  VtuberGatewayDeps,
  VtuberStripeClient,
  VtuberWebhookEventStore,
} from "../../src/runtime/deps.js";
import { createInMemorySessionStore } from "../../src/service/sessions/inMemorySessionStore.js";

function fakeCfg() {
  return ConfigSchema.parse({
    listenPort: 3001,
    logLevel: "fatal",
    brokerUrl: "http://broker:8080",
    payerDaemonSocket: "/var/run/livepeer/payer-daemon.sock",
    payerDefaultFaceValueWei: "1000000000000000",
    vtuberDefaultOffering: "default",
    vtuberSessionDefaultTtlSeconds: 3600,
    vtuberSessionBearerTtlSeconds: 7200,
    vtuberRateCardUsdPerSecond: "0.01",
    vtuberWorkerCallTimeoutMs: 15000,
    vtuberRelayMaxPerSession: 8,
    vtuberSessionBearerPepper: "this-is-a-test-pepper-min-16-chars",
    vtuberWorkerControlBearerPepper: "another-test-pepper-min-16-chars",
  });
}

function fakeStripe(): VtuberStripeClient & {
  checkoutCalls: Array<{ customerId: string; amountUsdCents: number }>;
  validSignatures: Map<string, { id: string; type: string; data: { object: Record<string, unknown> } }>;
} {
  const checkoutCalls: Array<{ customerId: string; amountUsdCents: number }> = [];
  const validSignatures = new Map<
    string,
    { id: string; type: string; data: { object: Record<string, unknown> } }
  >();
  return {
    checkoutCalls,
    validSignatures,
    async createCheckoutSession(input) {
      checkoutCalls.push({
        customerId: input.customerId,
        amountUsdCents: input.amountUsdCents,
      });
      return {
        sessionId: `cs_test_${checkoutCalls.length}`,
        url: `https://checkout.stripe.test/cs_test_${checkoutCalls.length}`,
      };
    },
    constructEvent(_rawBody, signature) {
      const evt = validSignatures.get(signature);
      if (!evt) {
        throw new Error("signature_invalid");
      }
      return evt;
    },
  };
}

function fakeWebhookStore(): VtuberWebhookEventStore & {
  inserted: Set<string>;
  credits: Array<{ customerId: string; stripeSessionId: string; amountUsdCents: bigint }>;
  disputes: string[];
} {
  const inserted = new Set<string>();
  const credits: Array<{
    customerId: string;
    stripeSessionId: string;
    amountUsdCents: bigint;
  }> = [];
  const disputes: string[] = [];
  return {
    inserted,
    credits,
    disputes,
    async insertIfNew(eventId, _type, _payload) {
      if (inserted.has(eventId)) return false;
      inserted.add(eventId);
      return true;
    },
    async creditTopup(input) {
      credits.push(input);
    },
    async markTopupDisputed(sessionId) {
      disputes.push(sessionId);
    },
  };
}

function fakeDeps(): VtuberGatewayDeps {
  return {
    cfg: fakeCfg(),
    sessionStore: createInMemorySessionStore(),
    authResolver: {
      async resolve() {
        return { id: "cust-77", tier: "prepaid", rateLimitTier: "default" };
      },
    },
    payerDaemon: {
      async createPayment() {
        throw new Error("unused");
      },
      async close() {},
    },
    serviceRegistry: {
      async listVtuberNodes() {
        return [];
      },
      async getNode() {
        return null;
      },
      async select() {
        return null;
      },
      async close() {},
    },
    worker: {
      async startSession() {
        throw new Error("unused");
      },
      async stopSession() {},
      async topupSession() {},
    },
  };
}

test("POST /v1/billing/topup mints a Stripe checkout URL", async () => {
  const deps = fakeDeps();
  const stripe = fakeStripe();
  const events = fakeWebhookStore();
  deps.stripe = stripe;
  deps.webhookEventStore = events;
  const app = await buildServer(deps);
  try {
    const resp = await app.inject({
      method: "POST",
      url: "/v1/billing/topup",
      headers: {
        "content-type": "application/json",
        authorization: "Bearer sk-test",
      },
      payload: {
        cents: 5_000,
        success_url: "https://example.com/ok",
        cancel_url: "https://example.com/cancel",
      },
    });
    assert.equal(resp.statusCode, 200);
    const body = resp.json() as {
      stripe_session_id: string;
      stripe_checkout_url: string;
    };
    assert.match(body.stripe_session_id, /^cs_test_/);
    assert.match(body.stripe_checkout_url, /^https:\/\/checkout\.stripe\.test\//);
    assert.equal(stripe.checkoutCalls.length, 1);
    assert.equal(stripe.checkoutCalls[0]!.customerId, "cust-77");
    assert.equal(stripe.checkoutCalls[0]!.amountUsdCents, 5_000);
  } finally {
    await app.close();
  }
});

test("POST /v1/billing/topup rejects amounts below the minimum", async () => {
  const deps = fakeDeps();
  deps.stripe = fakeStripe();
  deps.webhookEventStore = fakeWebhookStore();
  const app = await buildServer(deps);
  try {
    const resp = await app.inject({
      method: "POST",
      url: "/v1/billing/topup",
      headers: {
        "content-type": "application/json",
        authorization: "Bearer sk-test",
      },
      payload: {
        cents: 100,
        success_url: "https://example.com/ok",
        cancel_url: "https://example.com/cancel",
      },
    });
    assert.equal(resp.statusCode, 400);
    assert.match(resp.payload, /amount_out_of_range/);
  } finally {
    await app.close();
  }
});

test("POST /v1/stripe/webhook credits a topup on checkout.session.completed", async () => {
  const deps = fakeDeps();
  const stripe = fakeStripe();
  const events = fakeWebhookStore();
  deps.stripe = stripe;
  deps.webhookEventStore = events;

  stripe.validSignatures.set("sig-good-1", {
    id: "evt_1",
    type: "checkout.session.completed",
    data: {
      object: {
        id: "cs_test_42",
        client_reference_id: "cust-99",
        amount_total: 5_000,
      },
    },
  });

  const app = await buildServer(deps);
  try {
    const resp = await app.inject({
      method: "POST",
      url: "/v1/stripe/webhook",
      headers: {
        "content-type": "application/json",
        "stripe-signature": "sig-good-1",
      },
      payload: JSON.stringify({ id: "evt_1" }),
    });
    assert.equal(resp.statusCode, 200);
    assert.match(resp.payload, /"outcome":"processed"/);
    assert.equal(events.credits.length, 1);
    assert.equal(events.credits[0]!.customerId, "cust-99");
    assert.equal(events.credits[0]!.amountUsdCents, 5_000n);
  } finally {
    await app.close();
  }
});

test("POST /v1/stripe/webhook rejects missing signatures with 400", async () => {
  const deps = fakeDeps();
  deps.stripe = fakeStripe();
  deps.webhookEventStore = fakeWebhookStore();
  const app = await buildServer(deps);
  try {
    const resp = await app.inject({
      method: "POST",
      url: "/v1/stripe/webhook",
      headers: { "content-type": "application/json" },
      payload: JSON.stringify({}),
    });
    assert.equal(resp.statusCode, 400);
  } finally {
    await app.close();
  }
});

test("POST /v1/stripe/webhook rejects bad signatures with 400", async () => {
  const deps = fakeDeps();
  deps.stripe = fakeStripe();
  deps.webhookEventStore = fakeWebhookStore();
  const app = await buildServer(deps);
  try {
    const resp = await app.inject({
      method: "POST",
      url: "/v1/stripe/webhook",
      headers: {
        "content-type": "application/json",
        "stripe-signature": "sig-bogus",
      },
      payload: JSON.stringify({}),
    });
    assert.equal(resp.statusCode, 400);
    assert.match(resp.payload, /signature_invalid/);
  } finally {
    await app.close();
  }
});

test("POST /v1/stripe/webhook is idempotent on duplicate event IDs", async () => {
  const deps = fakeDeps();
  const stripe = fakeStripe();
  const events = fakeWebhookStore();
  deps.stripe = stripe;
  deps.webhookEventStore = events;

  stripe.validSignatures.set("sig-good-2", {
    id: "evt_2",
    type: "checkout.session.completed",
    data: {
      object: {
        id: "cs_test_77",
        client_reference_id: "cust-99",
        amount_total: 1_000,
      },
    },
  });

  const app = await buildServer(deps);
  try {
    const r1 = await app.inject({
      method: "POST",
      url: "/v1/stripe/webhook",
      headers: {
        "content-type": "application/json",
        "stripe-signature": "sig-good-2",
      },
      payload: JSON.stringify({ id: "evt_2" }),
    });
    assert.equal(r1.statusCode, 200);
    const r2 = await app.inject({
      method: "POST",
      url: "/v1/stripe/webhook",
      headers: {
        "content-type": "application/json",
        "stripe-signature": "sig-good-2",
      },
      payload: JSON.stringify({ id: "evt_2" }),
    });
    assert.equal(r2.statusCode, 200);
    assert.match(r2.payload, /"outcome":"duplicate"/);
    assert.equal(events.credits.length, 1, "topup credited exactly once");
  } finally {
    await app.close();
  }
});

test("POST /v1/vtuber/sessions/:id/end and topup happy path", async () => {
  const deps = fakeDeps();
  const node = {
    nodeId: "node-1",
    nodeUrl: "http://node-1:8080",
    ethAddress: "0x" + "f".repeat(40),
    capabilities: ["livepeer:vtuber-session"],
  };
  deps.serviceRegistry = {
    async listVtuberNodes() {
      return [node];
    },
    async getNode() {
      return node;
    },
    async select() {
      return node;
    },
    async close() {},
  };
  let topupCalls = 0;
  let stopCalls = 0;
  deps.worker = {
    async startSession() {
      throw new Error("unused");
    },
    async stopSession() {
      stopCalls += 1;
    },
    async topupSession() {
      topupCalls += 1;
    },
  };
  let payCalls = 0;
  deps.payerDaemon = {
    async createPayment() {
      payCalls += 1;
      return { payerWorkId: `work-${payCalls}`, paymentHeader: "lp-stub" };
    },
    async close() {},
  };

  const cfg = deps.cfg;
  const { mintSessionBearer } = await import(
    "../../src/service/auth/sessionBearer.js"
  );
  const minted = mintSessionBearer(cfg.vtuberSessionBearerPepper);
  const sessionId = "00000000-0000-0000-0000-00000000eeee";
  await deps.sessionStore.insertSession({
    id: sessionId,
    customerId: "cust-1",
    paramsJson: "{}",
    nodeId: node.nodeId,
    nodeUrl: node.nodeUrl,
    controlUrl: `ws://gw/v1/vtuber/sessions/${sessionId}/control`,
    expiresAt: new Date(Date.now() + 60_000),
  });
  await deps.sessionStore.insertBearer({
    sessionId,
    customerId: "cust-1",
    hash: minted.hash,
  });
  await deps.sessionStore.updateSession(sessionId, {
    status: "active",
    workerSessionId: "worker-sess-eeee",
  });

  const app = await buildServer(deps);
  try {
    const topup = await app.inject({
      method: "POST",
      url: `/v1/vtuber/sessions/${sessionId}/topup`,
      headers: {
        "content-type": "application/json",
        authorization: `Bearer ${minted.bearer}`,
      },
      payload: { cents: 100 },
    });
    assert.equal(topup.statusCode, 200);
    assert.equal(payCalls, 1);
    assert.equal(topupCalls, 1);
    const end = await app.inject({
      method: "POST",
      url: `/v1/vtuber/sessions/${sessionId}/end`,
      headers: {
        "content-type": "application/json",
        authorization: `Bearer ${minted.bearer}`,
      },
      payload: {},
    });
    assert.equal(end.statusCode, 200);
    assert.equal(stopCalls, 1);
    const row = await deps.sessionStore.findById(sessionId);
    assert.equal(row?.status, "ended");
    assert.notEqual(row?.endedAt, null);
  } finally {
    await app.close();
  }
});
