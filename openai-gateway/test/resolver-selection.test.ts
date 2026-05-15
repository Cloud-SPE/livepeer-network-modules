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
import { HEADER } from "../src/livepeer/headers.js";

interface CapturedBrokerCall {
  offering: string;
  requestId: string | null;
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

async function startStubBroker(captures: CapturedBrokerCall[]): Promise<{ app: FastifyInstance; url: string }> {
  const app = Fastify({ logger: false });
  app.addContentTypeParser(
    "application/json",
    { parseAs: "buffer" },
    (_req, body, done) => done(null, body),
  );
  app.post("/v1/cap", async (req, reply) => {
    const off = req.headers[HEADER.OFFERING.toLowerCase()] as string | undefined;
    const rid = req.headers[HEADER.REQUEST_ID.toLowerCase()] as string | undefined;
    captures.push({ offering: off ?? "", requestId: rid ?? null });
    await reply.code(200).header("Content-Type", "application/json").send({ ok: true });
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
  brokers: { east: string; west: string },
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

  const server = new grpc.Server();
  server.addService(proto.livepeer.registry.v1.Resolver.service, {
    listKnown: (_call: unknown, cb: grpc.sendUnaryData<unknown>) =>
      cb(null, {
        entries: [
          { ethAddress: "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" },
          { ethAddress: "0x9999999999999999999999999999999999999999" },
        ],
      }),
    resolveByAddress: (call: { request: { ethAddress: string } }, cb: grpc.sendUnaryData<unknown>) => {
      if (call.request.ethAddress.startsWith("0xeeee")) {
        cb(null, {
          nodes: [
            {
              url: brokers.east,
              operatorAddress: "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
              enabled: true,
              extraJson: Buffer.from(JSON.stringify({ geo: { region: "us-east-1" }, provider: "vllm" })),
              capabilities: [
                {
                  name: "openai:chat-completions",
                  workUnit: "tokens",
                  extraJson: Buffer.from(JSON.stringify({ interaction_mode: "http-reqresp@v0" })),
                  offerings: [
                    {
                      id: "model-small",
                      pricePerWorkUnitWei: "200",
                      constraintsJson: Buffer.from(JSON.stringify({ gpu: "l40s", quantization: "fp8" })),
                    },
                  ],
                },
              ],
            },
          ],
        });
        return;
      }

      cb(null, {
        nodes: [
          {
            url: brokers.west,
            operatorAddress: "0x9999999999999999999999999999999999999999",
            enabled: true,
            extraJson: Buffer.from(JSON.stringify({ geo: { region: "us-west-2" }, provider: "vllm" })),
            capabilities: [
              {
                name: "openai:chat-completions",
                workUnit: "tokens",
                extraJson: Buffer.from(JSON.stringify({ interaction_mode: "http-stream@v0" })),
                offerings: [
                  {
                    id: "model-small",
                    pricePerWorkUnitWei: "100",
                    constraintsJson: Buffer.from(JSON.stringify({ gpu: "a100", quantization: "fp8" })),
                  },
                ],
              },
            ],
          },
        ],
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

test("resolver-backed selection honors interaction mode before price and still applies extra/constraints", async (t) => {
  const paymentProtoRoot = await locatePaymentProtoRoot();
  const resolverProtoRoot = await locateResolverProtoRoot();
  if (!paymentProtoRoot || !resolverProtoRoot) {
    t.diagnostic("skipping: proto roots not found");
    return;
  }

  const tmpDir = await fs.mkdtemp(path.join(tmpdir(), "openai-gateway-resolver-"));
  const payerSock = path.join(tmpDir, "payer.sock");
  const resolverSock = path.join(tmpDir, "resolver.sock");

  const eastCaptures: CapturedBrokerCall[] = [];
  const westCaptures: CapturedBrokerCall[] = [];
  const eastBroker = await startStubBroker(eastCaptures);
  const westBroker = await startStubBroker(westCaptures);
  const payerSrv = await startStubPayerDaemon(payerSock, paymentProtoRoot);
  const resolverSrv = await startStubResolver(resolverSock, resolverProtoRoot, {
    east: eastBroker.url,
    west: westBroker.url,
  });

  await payment.init({ socketPath: payerSock, protoRoot: paymentProtoRoot });

  const cfg: Config = {
    brokerUrl: null,
    resolverSocket: resolverSock,
    recipientHex: null,
    listenPort: 0,
    databaseUrl: 'postgres://test:test@localhost:5432/test',
    authPepper: 'test-pepper',
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
    routeFailureThreshold: 2,
    routeCooldownMs: 30_000,
  };
  const server = await buildServer({ cfg });
  await server.listen({ host: "127.0.0.1", port: 0 });

  t.after(async () => {
    await server.close();
    await eastBroker.app.close();
    await westBroker.app.close();
    await new Promise<void>((res) => payerSrv.tryShutdown(() => res()));
    await new Promise<void>((res) => resolverSrv.tryShutdown(() => res()));
    payment.shutdown();
    await fs.rm(tmpDir, { recursive: true, force: true });
  });

  const addr = server.server.address();
  if (!addr || typeof addr === "string") throw new Error("no listen address");
  const base = `http://127.0.0.1:${addr.port}`;

  const preferredResp = await fetch(`${base}/v1/chat/completions`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      [HEADER.SELECTOR_EXTRA]: JSON.stringify({ geo: { region: "us-east-1" } }),
      [HEADER.SELECTOR_CONSTRAINTS]: JSON.stringify({ gpu: "l40s" }),
      [HEADER.REQUEST_ID]: "resolver-pref-1",
    },
    body: JSON.stringify({ model: "model-small", messages: [{ role: "user", content: "hi" }] }),
  });
  assert.equal(preferredResp.status, 200);
  assert.equal(eastCaptures.length, 1);
  assert.equal(westCaptures.length, 0);
  assert.equal(eastCaptures[0]?.requestId, "resolver-pref-1");

  const streamResp = await fetch(`${base}/v1/chat/completions`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      [HEADER.REQUEST_ID]: "resolver-stream-1",
    },
    body: JSON.stringify({ model: "model-small", messages: [{ role: "user", content: "hi again" }], stream: true }),
  });
  assert.equal(streamResp.status, 200);
  assert.equal(westCaptures.length, 1);
  assert.equal(westCaptures[0]?.requestId, "resolver-stream-1");
});
