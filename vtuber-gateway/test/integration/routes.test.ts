import test from "node:test";
import assert from "node:assert/strict";

import { ConfigSchema } from "../../src/config.js";
import { RouteHealthTracker } from "../../src/providers/routeHealth.js";
import { buildServer } from "../../src/server.js";
import type { VtuberGatewayDeps } from "../../src/runtime/deps.js";
import { createInMemorySessionStore } from "../../src/service/sessions/inMemorySessionStore.js";
import {
  hashSessionBearer,
  mintSessionBearer,
} from "../../src/service/auth/sessionBearer.js";

function testConfig() {
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
    routeFailureThreshold: 2,
    routeCooldownMs: 30000,
  });
}

interface FakeOpts {
  withWorker?: boolean;
  payerFails?: boolean;
  workerStartFails?: boolean;
  workerTopupFails?: boolean;
  workerStopFails?: boolean;
}

function buildDeps(opts: FakeOpts = {}): VtuberGatewayDeps {
  const cfg = testConfig();
  const sessionStore = createInMemorySessionStore();
  const node = {
    nodeId: "node-vtuber-1",
    nodeUrl: "http://node-vtuber-1:8080",
    ethAddress: "0xabc0000000000000000000000000000000000000",
    capabilities: ["livepeer:vtuber-session"],
  };
  return {
    cfg,
    sessionStore,
    authResolver: {
      async resolve() {
        return {
          id: "00000000-0000-4000-8000-00000000abcd",
          tier: "prepaid",
          rateLimitTier: "default",
        };
      },
    },
    payerDaemon: {
      async createPayment() {
        if (opts.payerFails === true) {
          throw new Error("payer-daemon offline");
        }
        return {
          payerWorkId: "work-1",
          paymentHeader: "lp-payment-header-stub",
        };
      },
      async close() {},
    },
    serviceRegistry: {
      async listVtuberNodes() {
        return opts.withWorker === false ? [] : [node];
      },
      async getNode() {
        return opts.withWorker === false ? null : node;
      },
      async select() {
        return opts.withWorker === false ? null : node;
      },
      async recordOutcome() {},
      async inspectHealth() {
        return [];
      },
      async inspectMetrics() {
        return {
          attemptsTotal: 0,
          successesTotal: 0,
          retryableFailuresTotal: 0,
          nonRetryableFailuresTotal: 0,
          cooldownsOpenedTotal: 0,
        };
      },
      async close() {},
    },
    worker: {
      async startSession(_url, _input) {
        if (opts.workerStartFails === true) {
          throw new Error("runner refused");
        }
        return {
          session_id: "worker-sess-1",
          status: "active",
          started_at: new Date().toISOString(),
        };
      },
      async stopSession() {
        if (opts.workerStopFails === true) {
          throw new Error("runner unreachable");
        }
      },
      async topupSession() {
        if (opts.workerTopupFails === true) {
          throw new Error("runner topup refused");
        }
      },
    },
  };
}

const VALID_OPEN_BODY = {
  persona: "grifter",
  vrm_url: "https://example.com/avatar.vrm",
  llm_provider: "livepeer",
  tts_provider: "livepeer",
};

test("GET /healthz returns ok", async () => {
  const app = await buildServer(buildDeps());
  try {
    const resp = await app.inject({ method: "GET", url: "/healthz" });
    assert.equal(resp.statusCode, 200);
  } finally {
    await app.close();
  }
});

test("POST /v1/vtuber/sessions rejects invalid bodies with 400", async () => {
  const app = await buildServer(buildDeps());
  try {
    const resp = await app.inject({
      method: "POST",
      url: "/v1/vtuber/sessions",
      headers: {
        "content-type": "application/json",
        authorization: "Bearer sk-test",
      },
      payload: { persona: "x" },
    });
    assert.equal(resp.statusCode, 400);
  } finally {
    await app.close();
  }
});

test("POST /v1/vtuber/sessions session-open happy path", async () => {
  const deps = buildDeps();
  const app = await buildServer(deps);
  try {
    const resp = await app.inject({
      method: "POST",
      url: "/v1/vtuber/sessions",
      headers: {
        "content-type": "application/json",
        authorization: "Bearer sk-test",
      },
      payload: VALID_OPEN_BODY,
    });
    assert.equal(resp.statusCode, 200);
    const body = resp.json() as {
      session_id: string;
      control_url: string;
      session_child_bearer: string;
    };
    assert.match(body.session_id, /[0-9a-f-]{36}/);
    assert.match(body.session_child_bearer, /^vtbs_/);
    const all = await deps.sessionStore.listSessions();
    assert.equal(all.length, 1);
    assert.equal(all[0]!.status, "active");
    assert.equal(all[0]!.workerSessionId, "worker-sess-1");
    assert.equal(all[0]!.payerWorkId, "work-1");
  } finally {
    await app.close();
  }
});

test("POST /v1/vtuber/sessions returns 503 + Retry-After when no worker is available", async () => {
  const deps = buildDeps({ withWorker: false });
  const app = await buildServer(deps);
  try {
    const resp = await app.inject({
      method: "POST",
      url: "/v1/vtuber/sessions",
      headers: {
        "content-type": "application/json",
        authorization: "Bearer sk-test",
      },
      payload: VALID_OPEN_BODY,
    });
    assert.equal(resp.statusCode, 503);
    assert.equal(resp.headers["retry-after"], "5");
    assert.equal(resp.headers["livepeer-error"], "no_worker_available");
    const all = await deps.sessionStore.listSessions();
    assert.equal(all.length, 0, "no session row inserted when no worker");
  } finally {
    await app.close();
  }
});

test("POST /v1/vtuber/sessions returns 502 + payment_emit_failed on payer failure", async () => {
  const deps = buildDeps({ payerFails: true });
  const app = await buildServer(deps);
  try {
    const resp = await app.inject({
      method: "POST",
      url: "/v1/vtuber/sessions",
      headers: {
        "content-type": "application/json",
        authorization: "Bearer sk-test",
      },
      payload: VALID_OPEN_BODY,
    });
    assert.equal(resp.statusCode, 502);
    assert.equal(resp.headers["livepeer-error"], "payment_emit_failed");
    const all = await deps.sessionStore.listSessions();
    assert.equal(all.length, 1);
    assert.equal(all[0]!.status, "errored");
    assert.equal(all[0]!.errorCode, "payment_emit_failed");
  } finally {
    await app.close();
  }
});

test("POST /v1/vtuber/sessions returns 502 + worker_start_failed on runner refusal", async () => {
  const deps = buildDeps({ workerStartFails: true });
  const app = await buildServer(deps);
  try {
    const resp = await app.inject({
      method: "POST",
      url: "/v1/vtuber/sessions",
      headers: {
        "content-type": "application/json",
        authorization: "Bearer sk-test",
      },
      payload: VALID_OPEN_BODY,
    });
    assert.equal(resp.statusCode, 502);
    assert.equal(resp.headers["livepeer-error"], "worker_start_failed");
    const all = await deps.sessionStore.listSessions();
    assert.equal(all[0]!.status, "errored");
    assert.equal(all[0]!.errorCode, "worker_start_failed");
  } finally {
    await app.close();
  }
});

test("POST /v1/vtuber/sessions cools a failing node and uses the next node on later requests", async () => {
  const deps = buildDeps();
  const tracker = new RouteHealthTracker({ failureThreshold: 1, cooldownMs: 60_000 });
  const nodeA = {
    nodeId: "node-a",
    nodeUrl: "http://node-a:8080",
    ethAddress: "0xaaa0000000000000000000000000000000000000",
    capabilities: ["livepeer:vtuber-session"],
    offering: "default",
  };
  const nodeB = {
    nodeId: "node-b",
    nodeUrl: "http://node-b:8080",
    ethAddress: "0xbbb0000000000000000000000000000000000000",
    capabilities: ["livepeer:vtuber-session"],
    offering: "default",
  };
  deps.serviceRegistry = {
    async listVtuberNodes() {
      return [nodeA, nodeB];
    },
    async getNode(nodeId: string) {
      return nodeId === nodeA.nodeId ? nodeA : nodeId === nodeB.nodeId ? nodeB : null;
    },
    async select() {
      return tracker.rankCandidates([nodeA, nodeB])[0] ?? null;
    },
    async recordOutcome(node, outcome, reason) {
      tracker.record(node, outcome, reason);
    },
    async inspectHealth() {
      return tracker.inspect();
    },
    async inspectMetrics() {
      return tracker.inspectMetrics();
    },
    async close() {},
  };
  const startedUrls: string[] = [];
  deps.worker = {
    async startSession(nodeUrl) {
      startedUrls.push(nodeUrl);
      if (nodeUrl === nodeA.nodeUrl) {
        throw new Error("runner refused");
      }
      return {
        session_id: "worker-sess-2",
        status: "active",
        started_at: new Date().toISOString(),
        control_url: "ws://gw.invalid/control",
        expires_at: new Date(Date.now() + 60_000).toISOString(),
      };
    },
    async stopSession() {},
    async topupSession() {},
  };
  const app = await buildServer(deps);
  try {
    const first = await app.inject({
      method: "POST",
      url: "/v1/vtuber/sessions",
      headers: {
        "content-type": "application/json",
        authorization: "Bearer sk-test",
      },
      payload: VALID_OPEN_BODY,
    });
    assert.equal(first.statusCode, 502);

    const second = await app.inject({
      method: "POST",
      url: "/v1/vtuber/sessions",
      headers: {
        "content-type": "application/json",
        authorization: "Bearer sk-test",
      },
      payload: VALID_OPEN_BODY,
    });
    assert.equal(second.statusCode, 200);
    assert.deepEqual(startedUrls, [nodeA.nodeUrl, nodeB.nodeUrl]);
  } finally {
    await app.close();
  }
});

test("POST /v1/vtuber/sessions rejects unauthenticated requests with 401", async () => {
  const deps = buildDeps();
  deps.authResolver = { async resolve() { return null; } };
  const app = await buildServer(deps);
  try {
    const resp = await app.inject({
      method: "POST",
      url: "/v1/vtuber/sessions",
      headers: { "content-type": "application/json" },
      payload: VALID_OPEN_BODY,
    });
    assert.equal(resp.statusCode, 401);
  } finally {
    await app.close();
  }
});

test("GET /v1/vtuber/sessions/:id rejects malformed UUIDs", async () => {
  const app = await buildServer(buildDeps());
  try {
    const resp = await app.inject({
      method: "GET",
      url: "/v1/vtuber/sessions/not-a-uuid",
      headers: { authorization: "Bearer vtbs_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" },
    });
    assert.equal(resp.statusCode, 401);
  } finally {
    await app.close();
  }
});

test("session-bearer-protected GET returns the session record", async () => {
  const deps = buildDeps();
  const cfg = deps.cfg;
  const minted = mintSessionBearer(cfg.vtuberSessionBearerPepper);
  const sessionId = "00000000-0000-4000-8000-00000000aaaa";
  const expiresAt = new Date(Date.now() + 60_000);
  await deps.sessionStore.insertSession({
    id: sessionId,
    customerId: "cust-1",
    paramsJson: "{}",
    nodeId: "n1",
    nodeUrl: "http://n1",
    controlUrl: "ws://gw/v1/vtuber/sessions/" + sessionId + "/control",
    expiresAt,
  });
  await deps.sessionStore.insertBearer({
    sessionId,
    customerId: "cust-1",
    hash: minted.hash,
  });
  void hashSessionBearer;
  const app = await buildServer(deps);
  try {
    const resp = await app.inject({
      method: "GET",
      url: `/v1/vtuber/sessions/${sessionId}`,
      headers: { authorization: `Bearer ${minted.bearer}` },
    });
    assert.equal(resp.statusCode, 200);
    const body = resp.json() as { session_id: string; status: string };
    assert.equal(body.session_id, sessionId);
    assert.equal(body.status, "starting");
  } finally {
    await app.close();
  }
});

test("POST /v1/vtuber/sessions/:id/topup rejects non-positive cents", async () => {
  const deps = buildDeps();
  const cfg = deps.cfg;
  const minted = mintSessionBearer(cfg.vtuberSessionBearerPepper);
  const sessionId = "00000000-0000-4000-8000-00000000aaab";
  await deps.sessionStore.insertSession({
    id: sessionId,
    customerId: "cust-1",
    paramsJson: "{}",
    nodeId: "n1",
    nodeUrl: "http://n1",
    controlUrl: "ws://gw/v1/vtuber/sessions/" + sessionId + "/control",
    expiresAt: new Date(Date.now() + 60_000),
  });
  await deps.sessionStore.insertBearer({
    sessionId,
    customerId: "cust-1",
    hash: minted.hash,
  });
  const app = await buildServer(deps);
  try {
    const resp = await app.inject({
      method: "POST",
      url: `/v1/vtuber/sessions/${sessionId}/topup`,
      headers: {
        "content-type": "application/json",
        authorization: `Bearer ${minted.bearer}`,
      },
      payload: { cents: -1 },
    });
    assert.equal(resp.statusCode, 400);
  } finally {
    await app.close();
  }
});

test("POST /v1/stripe/webhook requires a stripe-signature header", async () => {
  const app = await buildServer(buildDeps());
  try {
    const resp = await app.inject({
      method: "POST",
      url: "/v1/stripe/webhook",
      payload: {},
    });
    assert.equal(resp.statusCode, 400);
  } finally {
    await app.close();
  }
});
