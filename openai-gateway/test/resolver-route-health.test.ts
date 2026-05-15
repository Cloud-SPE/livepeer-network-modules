import test from "node:test";
import assert from "node:assert/strict";
import { tmpdir } from "node:os";
import path from "node:path";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";

import Fastify, { type FastifyInstance } from "fastify";
import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";

import * as payment from "../src/livepeer/payment.js";
import { buildServer } from "../src/server.js";
import type { Config } from "../src/config.js";

interface BrokerCapture {
  requests: number;
}

async function dirExists(p: string): Promise<boolean> {
  try {
    return (await fs.stat(p)).isDirectory();
  } catch {
    return false;
  }
}

async function locatePaymentProtoRoot(): Promise<string | null> {
  let dir = path.dirname(fileURLToPath(import.meta.url));
  for (let i = 0; i < 10; i++) {
    const candidate = path.join(dir, "livepeer-network-protocol", "proto");
    if (await dirExists(candidate)) return candidate;
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  return null;
}

async function locateResolverProtoRoot(): Promise<string | null> {
  let dir = path.dirname(fileURLToPath(import.meta.url));
  for (let i = 0; i < 10; i++) {
    const candidate = path.join(dir, "proto-contracts");
    if (await dirExists(candidate)) return candidate;
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  return null;
}

async function startBroker(
  capture: BrokerCapture,
  statusCode: number,
): Promise<{ app: FastifyInstance; url: string }> {
  const app = Fastify({ logger: false });
  app.post("/v1/cap", async (_req, reply) => {
    capture.requests += 1;
    await reply.code(statusCode).header("Content-Type", "application/json").send(
      statusCode >= 500 ? { message: "backend unavailable" } : { ok: true },
    );
  });
  await app.listen({ host: "127.0.0.1", port: 0 });
  const addr = app.server.address();
  if (!addr || typeof addr === "string") throw new Error("no listen address");
  return { app, url: `http://127.0.0.1:${addr.port}` };
}

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
        paymentBytes: Buffer.from("stub-payment-bytes"),
        ticketsCreated: 1,
        expectedValue: Buffer.from([0x03, 0xe8]),
      }),
    getDepositInfo: (_call: unknown, cb: grpc.sendUnaryData<unknown>) => cb(null, {}),
    getSessionDebits: (_call: unknown, cb: grpc.sendUnaryData<unknown>) =>
      cb(null, { totalWorkUnits: 0, debitCount: 0, closed: false }),
    health: (_call: unknown, cb: grpc.sendUnaryData<unknown>) => cb(null, { status: "ok" }),
  });
  await new Promise<void>((res, rej) => {
    server.bindAsync(`unix:${socketPath}`, grpc.ServerCredentials.createInsecure(), (err) =>
      err ? rej(err) : res(),
    );
  });
  return server;
}

async function startStubResolver(
  socketPath: string,
  protoRoot: string,
  brokers: { primary: string; secondary: string },
): Promise<grpc.Server> {
  const def = await protoLoader.load(
    ["livepeer/registry/v1/types.proto", "livepeer/registry/v1/resolver.proto"],
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
    livepeer: { registry: { v1: { Resolver: { service: grpc.ServiceDefinition } } } };
  };

  const nodes = [
    {
      url: brokers.primary,
      operatorAddress: "0x1111111111111111111111111111111111111111",
    },
    {
      url: brokers.secondary,
      operatorAddress: "0x2222222222222222222222222222222222222222",
    },
  ];

  const server = new grpc.Server();
  server.addService(proto.livepeer.registry.v1.Resolver.service, {
    listKnown: (_call: unknown, cb: grpc.sendUnaryData<unknown>) =>
      cb(null, {
        entries: nodes.map((node) => ({ ethAddress: node.operatorAddress })),
      }),
    resolveByAddress: (call: { request: { ethAddress: string } }, cb: grpc.sendUnaryData<unknown>) => {
      const node = nodes.find((entry) => entry.operatorAddress === call.request.ethAddress);
      const pricePerWorkUnitWei =
        call.request.ethAddress === "0x1111111111111111111111111111111111111111" ? "100" : "200";
      cb(null, {
        nodes: node
          ? [
              {
                url: node.url,
                operatorAddress: node.operatorAddress,
                enabled: true,
                extraJson: Buffer.from(JSON.stringify({ provider: "vllm" })),
                capabilities: [
                  {
                    name: "openai:chat-completions",
                    workUnit: "tokens",
                    extraJson: Buffer.from(JSON.stringify({ interaction_mode: "http-reqresp@v0" })),
                    offerings: [
                      {
                        id: "model-small",
                        pricePerWorkUnitWei,
                        constraintsJson: Buffer.from(JSON.stringify({ gpu: "a100" })),
                      },
                    ],
                  },
                ],
              },
            ]
          : [],
      });
    },
    refresh: (_call: unknown, cb: grpc.sendUnaryData<unknown>) => cb(null, {}),
    getAuditLog: (_call: unknown, cb: grpc.sendUnaryData<unknown>) => cb(null, { events: [] }),
    health: (_call: unknown, cb: grpc.sendUnaryData<unknown>) =>
      cb(null, {
        mode: "resolver",
        chainOk: true,
        manifestFetcherOk: true,
        cacheSize: 2,
      }),
  });
  await new Promise<void>((res, rej) => {
    server.bindAsync(`unix:${socketPath}`, grpc.ServerCredentials.createInsecure(), (err) =>
      err ? rej(err) : res(),
    );
  });
  return server;
}

test("resolver-backed routing cools down failing routes after retryable broker errors", async (t) => {
  const paymentProtoRoot = await locatePaymentProtoRoot();
  const resolverProtoRoot = await locateResolverProtoRoot();
  if (!paymentProtoRoot || !resolverProtoRoot) {
    t.diagnostic("skipping: proto roots not found");
    return;
  }

  const tmpDir = await fs.mkdtemp(path.join(tmpdir(), "openai-gateway-route-health-"));
  const payerSock = path.join(tmpDir, "payer.sock");
  const resolverSock = path.join(tmpDir, "resolver.sock");

  const primaryCapture: BrokerCapture = { requests: 0 };
  const secondaryCapture: BrokerCapture = { requests: 0 };
  const primary = await startBroker(primaryCapture, 503);
  const secondary = await startBroker(secondaryCapture, 200);
  const payerSrv = await startStubPayerDaemon(payerSock, paymentProtoRoot);
  const resolverSrv = await startStubResolver(resolverSock, resolverProtoRoot, {
    primary: primary.url,
    secondary: secondary.url,
  });

  await payment.init({ socketPath: payerSock, protoRoot: paymentProtoRoot });

  const cfg: Config = {
    brokerUrl: null,
    resolverSocket: resolverSock,
    recipientHex: null,
    listenPort: 0,
    databaseUrl: "postgres://test:test@localhost:5432/test",
    authPepper: "test-pepper",
    adminTokens: [],
    publicBaseUrl: null,
    stripe: null,
    defaultOffering: "default",
    payerDaemonSocket: payerSock,
    paymentProtoRoot,
    resolverProtoRoot,
    resolverSnapshotTtlMs: 15_000,
    offeringsConfigPath: "/dev/null",
    offerings: { defaults: {} },
    audioSpeechEnabled: false,
    brokerCallTimeoutMs: 30_000,
    routeFailureThreshold: 1,
    routeCooldownMs: 60_000,
  };
  const server = await buildServer({ cfg });
  await server.listen({ host: "127.0.0.1", port: 0 });

  t.after(async () => {
    await server.close();
    await primary.app.close();
    await secondary.app.close();
    await new Promise<void>((res) => payerSrv.tryShutdown(() => res()));
    await new Promise<void>((res) => resolverSrv.tryShutdown(() => res()));
    payment.shutdown();
    await fs.rm(tmpDir, { recursive: true, force: true });
  });

  const addr = server.server.address();
  if (!addr || typeof addr === "string") throw new Error("no listen address");
  const base = `http://127.0.0.1:${addr.port}`;

  const first = await fetch(`${base}/v1/chat/completions`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ model: "model-small", messages: [{ role: "user", content: "first" }] }),
  });
  assert.equal(first.status, 200);
  assert.equal(primaryCapture.requests, 1);
  assert.equal(secondaryCapture.requests, 1);

  const second = await fetch(`${base}/v1/chat/completions`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ model: "model-small", messages: [{ role: "user", content: "second" }] }),
  });
  assert.equal(second.status, 200);
  assert.equal(primaryCapture.requests, 1);
  assert.equal(secondaryCapture.requests, 2);
});
