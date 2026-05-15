import test from "node:test";
import assert from "node:assert/strict";
import { spawn, type ChildProcessByStdio } from "node:child_process";
import type { Readable } from "node:stream";
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

interface HealthState {
  status: "ready" | "degraded";
  staleAfterIso: string;
}

async function dirExists(p: string): Promise<boolean> {
  try {
    return (await fs.stat(p)).isDirectory();
  } catch {
    return false;
  }
}

async function fileExists(p: string): Promise<boolean> {
  try {
    await fs.stat(p);
    return true;
  } catch {
    return false;
  }
}

async function locateRepoRoot(): Promise<string | null> {
  let dir = path.dirname(fileURLToPath(import.meta.url));
  for (let i = 0; i < 12; i++) {
    const candidate = path.join(dir, "service-registry-daemon");
    if (await dirExists(candidate)) return dir;
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  return null;
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

async function waitFor(
  predicate: () => Promise<boolean> | boolean,
  timeoutMs: number,
  label: string,
): Promise<void> {
  const started = Date.now();
  while (Date.now() - started < timeoutMs) {
    if (await predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw new Error(`timed out waiting for ${label}`);
}

async function startBroker(
  capture: BrokerCapture,
  health: HealthState,
): Promise<{ app: FastifyInstance; url: string }> {
  const app = Fastify({ logger: false });
  app.addContentTypeParser(
    "application/json",
    { parseAs: "buffer" },
    (_req, body, done) => done(null, body),
  );
  app.get("/registry/health", async (_req, reply) => {
    await reply.code(200).header("Content-Type", "application/json").send({
      broker_status: "ready",
      generated_at: new Date().toISOString(),
      capabilities: [
        {
          id: "openai:chat-completions",
          offering_id: "model-small",
          status: health.status,
          stale_after: health.staleAfterIso,
        },
      ],
    });
  });
  app.post("/v1/cap", async (_req, reply) => {
    capture.requests += 1;
    await reply.code(200).header("Content-Type", "application/json").send({
      ok: true,
      provider: "integration-broker",
    });
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

async function startActualResolverDaemon(
  repoRoot: string,
  socketPath: string,
  overlayPath: string,
): Promise<{ child: ChildProcessByStdio<null, Readable, Readable>; stderr: string[] }> {
  const stderr: string[] = [];
  const child = spawn(
    "go",
    [
      "run",
      "./cmd/livepeer-service-registry-daemon",
      "--mode=resolver",
      "--dev",
      `--socket=${socketPath}`,
      `--static-overlay=${overlayPath}`,
      "--log-level=debug",
    ],
    {
      cwd: path.join(repoRoot, "service-registry-daemon"),
      detached: true,
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  child.stderr.setEncoding("utf8");
  child.stderr.on("data", (chunk: string) => stderr.push(chunk));
  child.stdout.resume();
  return { child, stderr };
}

async function stopChild(child: ChildProcessByStdio<null, Readable, Readable>): Promise<void> {
  if (child.pid) {
    try {
      process.kill(-child.pid, "SIGTERM");
    } catch {}
  } else if (child.exitCode === null && !child.killed) {
    child.kill("SIGTERM");
  }
  await Promise.race([
    new Promise<void>((resolve) => child.once("close", () => resolve())),
    new Promise<void>((resolve) =>
      setTimeout(() => {
        if (child.pid) {
          try {
            process.kill(-child.pid, "SIGKILL");
          } catch {}
        } else if (child.exitCode === null) {
          child.kill("SIGKILL");
        }
        resolve();
      }, 2_000),
    ),
  ]);
}

test("openai-gateway uses actual resolver daemon live health before dispatch", async (t) => {
  const repoRoot = await locateRepoRoot();
  const paymentProtoRoot = await locatePaymentProtoRoot();
  const resolverProtoRoot = await locateResolverProtoRoot();
  if (!repoRoot || !paymentProtoRoot || !resolverProtoRoot) {
    t.diagnostic("skipping: required repo roots not found");
    return;
  }

  const tmpDir = await fs.mkdtemp(path.join(tmpdir(), "openai-gateway-daemon-health-"));
  const payerSock = path.join(tmpDir, "payer.sock");
  const resolverSock = path.join(tmpDir, "resolver.sock");
  const overlayPath = path.join(tmpDir, "overlay.yaml");

  const brokerCapture: BrokerCapture = { requests: 0 };
  const health: HealthState = {
    status: "degraded",
    staleAfterIso: new Date(Date.now() + 75).toISOString(),
  };
  const broker = await startBroker(brokerCapture, health);
  const payerSrv = await startStubPayerDaemon(payerSock, paymentProtoRoot);

  const overlay = `overlay:
  - eth_address: "0x1111111111111111111111111111111111111111"
    enabled: true
    unsigned_allowed: true
    pin:
      - id: "orch-a"
        url: "${broker.url}"
        capabilities:
          - name: "openai:chat-completions"
            work_unit: "tokens"
            offerings:
              - id: "model-small"
                price_per_work_unit_wei: "100"
`;
  await fs.writeFile(overlayPath, overlay, "utf8");

  const { child, stderr } = await startActualResolverDaemon(repoRoot, resolverSock, overlayPath);
  t.after(async () => {
    await stopChild(child);
    await broker.app.close();
    payment.shutdown();
    await new Promise<void>((resolve) => payerSrv.tryShutdown(() => resolve()));
  });

  await waitFor(() => fileExists(resolverSock), 20_000, "resolver socket");
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
    resolverSnapshotTtlMs: 1,
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
  });

  const serverAddr = server.server.address();
  if (!serverAddr || typeof serverAddr === "string") {
    throw new Error("no gateway listen address");
  }
  const base = `http://127.0.0.1:${serverAddr.port}`;

  const first = await fetch(`${base}/v1/chat/completions`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      model: "model-small",
      messages: [{ role: "user", content: "hello" }],
    }),
  });
  assert.equal(first.status, 500);
  assert.equal(brokerCapture.requests, 0, `broker should not receive a request while live health is red; daemon stderr=${stderr.join("")}`);
  const firstBody = await first.json() as { error: string; message: string };
  assert.equal(firstBody.error, "internal_error");
  assert.match(firstBody.message, /no route candidates/);

  await new Promise((resolve) => setTimeout(resolve, 125));
  health.status = "ready";
  health.staleAfterIso = new Date(Date.now() + 60_000).toISOString();

  const second = await fetch(`${base}/v1/chat/completions`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      model: "model-small",
      messages: [{ role: "user", content: "hello again" }],
    }),
  });
  assert.equal(second.status, 200, `expected dispatch to recover after live health turns ready; daemon stderr=${stderr.join("")}`);
  assert.equal(brokerCapture.requests, 1);
  const secondBody = await second.json() as { ok: boolean };
  assert.equal(secondBody.ok, true);
});
