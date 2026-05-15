import test from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import path from "node:path";
import fs from "node:fs/promises";

import Fastify from "fastify";
import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";
import { WebSocketServer } from "ws";

import type { Config } from "../../src/config.js";
import type { OrchCandidate, OrchSelector } from "../../src/orchSelector.js";
import { init as initPayer, shutdown as shutdownPayer } from "../../src/paymentClient.js";
import { RouteHealthTracker } from "../../src/routeHealth.js";
import { registerOrchRoutes } from "../../src/routes/orchs.js";
import { registerSessionRoutes } from "../../src/routes/sessions.js";
import { SessionRouter } from "../../src/sessionRouter.js";

const cfg: Config = {
  listen: ":9100",
  payerDaemonSocket: "/tmp/payer.sock",
  resolverSocket: "/tmp/resolver.sock",
  capabilityId: "daydream:scope:v1",
  offeringId: "default",
  interactionMode: "session-control-external-media@v0",
  resolverSnapshotTtlMs: 30_000,
  paymentProtoRoot: "/tmp/proto",
  resolverProtoRoot: "/tmp/proto-contracts",
  routeFailureThreshold: 1,
  routeCooldownMs: 60_000,
};

test("daydream session-open retries next orch and exposes cooled route state", async (t) => {
  const tmpDir = await fs.mkdtemp(path.join(tmpdir(), "daydream-gateway-test-"));
  const payerSock = path.join(tmpDir, "payer.sock");
  const tracker = new RouteHealthTracker({ failureThreshold: 1, cooldownMs: 60_000 });
  const candidates: OrchCandidate[] = [
    {
      brokerUrl: "http://broker-a.invalid:8080",
      ethAddress: "0xaaa0000000000000000000000000000000000000",
      capability: "daydream:scope:v1",
      offering: "default",
      workUnit: "sessions",
      pricePerWorkUnitWei: "1",
    },
    {
      brokerUrl: "http://broker-b.invalid:8080",
      ethAddress: "0xbbb0000000000000000000000000000000000000",
      capability: "daydream:scope:v1",
      offering: "default",
      workUnit: "sessions",
      pricePerWorkUnitWei: "2",
    },
  ];
  const selector: OrchSelector = {
    async list() {
      return candidates;
    },
    async pickRandom() {
      const choice = tracker.chooseRandom(candidates);
      if (!choice) {
        throw new Error("no candidates");
      }
      return choice;
    },
    recordOutcome(candidate, outcome, reason) {
      tracker.record(candidate, outcome, reason);
    },
    inspectHealth() {
      return tracker.inspect();
    },
    inspectMetrics() {
      return tracker.inspectMetrics();
    },
  };
  const router = new SessionRouter();
  const app = Fastify({ logger: false });
  registerOrchRoutes(app, selector);
  registerSessionRoutes(app, cfg, selector, router);

  const server = createServer();
  const wss = new WebSocketServer({ server });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", () => resolve()));
  const address = server.address();
  if (!address || typeof address === "string") {
    throw new Error("missing websocket address");
  }
  const controlUrl = `ws://127.0.0.1:${address.port}/control`;
  const paymentProtoRoot = path.resolve("..", "livepeer-network-protocol", "proto");
  const payerSrv = await startStubPayerDaemon(payerSock, paymentProtoRoot);
  await initPayer({
    socketPath: payerSock,
    protoRoot: paymentProtoRoot,
  });

  let brokerACalls = 0;
  let brokerBCalls = 0;
  const fetchBefore = globalThis.fetch;
  const randomBefore = Math.random;
  Math.random = () => 0;
  globalThis.fetch = (async (input) => {
    const url = String(input);
    if (url.startsWith("http://broker-a.invalid:8080")) {
      brokerACalls += 1;
      return new Response("down", { status: 503 });
    }
    brokerBCalls += 1;
    return new Response(JSON.stringify({
      session_id: `sess_${brokerBCalls}`,
      control_url: controlUrl,
      media: {
        schema: "scope",
        scope_url: "https://scope.example.com/session",
      },
      expires_at: "2026-05-15T00:00:00Z",
    }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;

  t.after(async () => {
    Math.random = randomBefore;
    globalThis.fetch = fetchBefore;
    for (const session of router.list()) {
      session.controlWs?.end();
    }
    for (const client of wss.clients) {
      client.close();
    }
    shutdownPayer();
    await new Promise<void>((resolve) => payerSrv.tryShutdown(() => resolve()));
    await new Promise<void>((resolve) => wss.close(() => resolve()));
    await new Promise<void>((resolve) => server.close(() => resolve()));
    await app.close();
    await fs.rm(tmpDir, { recursive: true, force: true });
  });

  const first = await app.inject({
    method: "POST",
    url: "/v1/sessions",
  });
  assert.equal(first.statusCode, 201, first.body);
  assert.equal(brokerACalls, 1);
  assert.equal(brokerBCalls, 1);

  const orchs = await app.inject({
    method: "GET",
    url: "/v1/orchs",
  });
  assert.equal(orchs.statusCode, 200);
  const orchBody = orchs.json() as {
    summary: {
      tracked_routes: number;
      cooling_routes: number;
      routes_with_failures: number;
    };
    metrics: {
      attemptsTotal: number;
      successesTotal: number;
      retryableFailuresTotal: number;
      nonRetryableFailuresTotal: number;
      cooldownsOpenedTotal: number;
    };
    orchs: Array<{ broker_url: string; route_health: { coolingDown?: boolean } | null }>;
  };
  assert.equal(orchBody.summary.tracked_routes, 2);
  assert.equal(orchBody.summary.cooling_routes, 1);
  assert.equal(orchBody.summary.routes_with_failures, 1);
  assert.equal(orchBody.metrics.attemptsTotal, 2);
  assert.equal(orchBody.metrics.successesTotal, 1);
  assert.equal(orchBody.metrics.retryableFailuresTotal, 1);
  assert.equal(orchBody.metrics.cooldownsOpenedTotal, 1);
  const brokerA = orchBody.orchs.find((entry) => entry.broker_url.includes("broker-a"));
  assert.equal(brokerA?.route_health?.coolingDown, true);

  const prom = await app.inject({
    method: "GET",
    url: "/v1/orchs/metrics",
  });
  assert.equal(prom.statusCode, 200);
  assert.match(prom.headers["content-type"] ?? "", /^text\/plain/);
  assert.match(prom.body, /livepeer_gateway_route_health_attempts_total\{gateway="daydream"\} 2/);
  assert.match(prom.body, /livepeer_gateway_route_health_cooldowns_opened_total\{gateway="daydream"\} 1/);

  const second = await app.inject({
    method: "POST",
    url: "/v1/sessions",
  });
  assert.equal(second.statusCode, 201);
  assert.equal(brokerACalls, 1, "cooled route should be skipped on subsequent sessions");
  assert.equal(brokerBCalls, 2);
});

async function startStubPayerDaemon(socketPath: string, protoRoot: string): Promise<grpc.Server> {
  const def = await protoLoader.load(
    ["livepeer/payments/v1/types.proto", "livepeer/payments/v1/payer_daemon.proto"],
    {
      keepCase: false,
      longs: String,
      enums: String,
      defaults: true,
      oneofs: true,
      includeDirs: [protoRoot],
    },
  );
  const proto = grpc.loadPackageDefinition(def) as unknown as {
    livepeer: { payments: { v1: { PayerDaemon: { service: grpc.ServiceDefinition } } } };
  };
  const server = new grpc.Server();
  server.addService(proto.livepeer.payments.v1.PayerDaemon.service, {
    createPayment: (_call: unknown, cb: grpc.sendUnaryData<unknown>) =>
      cb(null, {
        paymentBytes: Buffer.from("stub-payment"),
        ticketsCreated: 1,
        expectedValue: Buffer.from([0x01]),
      }),
    getDepositInfo: (_call: unknown, cb: grpc.sendUnaryData<unknown>) => cb(null, {}),
    getSessionDebits: (_call: unknown, cb: grpc.sendUnaryData<unknown>) =>
      cb(null, { totalWorkUnits: 0, debitCount: 0, closed: false }),
    health: (_call: unknown, cb: grpc.sendUnaryData<unknown>) => cb(null, { status: "ok" }),
  });
  await new Promise<void>((resolve, reject) => {
    server.bindAsync(`unix:${socketPath}`, grpc.ServerCredentials.createInsecure(), (err) =>
      err ? reject(err) : resolve(),
    );
  });
  return server;
}
