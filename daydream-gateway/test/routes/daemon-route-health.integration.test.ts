import test from "node:test";
import assert from "node:assert/strict";
import { spawn, type ChildProcessByStdio } from "node:child_process";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import path from "node:path";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import type { Readable } from "node:stream";

import Fastify, { type FastifyInstance } from "fastify";
import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";
import { WebSocketServer } from "ws";

import type { Config } from "../../src/config.js";
import { createOrchSelector } from "../../src/orchSelector.js";
import { init as initPayer, shutdown as shutdownPayer } from "../../src/paymentClient.js";
import { registerOrchRoutes } from "../../src/routes/orchs.js";
import { registerSessionRoutes } from "../../src/routes/sessions.js";
import { SessionRouter } from "../../src/sessionRouter.js";

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
  controlUrl: string,
): Promise<{ app: FastifyInstance; url: string }> {
  const app = Fastify({ logger: false });
  app.get("/registry/health", async (_req, reply) => {
    await reply.code(200).header("Content-Type", "application/json").send({
      broker_status: "ready",
      generated_at: new Date().toISOString(),
      capabilities: [
        {
          id: "daydream:scope:v1",
          offering_id: "default",
          status: health.status,
          stale_after: health.staleAfterIso,
        },
      ],
    });
  });
  app.post("/v1/cap", async (_req, reply) => {
    capture.requests += 1;
    await reply.code(200).header("Content-Type", "application/json").send({
      session_id: `sess_${capture.requests}`,
      control_url: controlUrl,
      media: {
        schema: "scope",
        scope_url: "https://scope.example.com/session",
      },
      expires_at: "2026-05-15T00:00:00Z",
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

test("daydream-gateway uses actual resolver daemon live health before session-open dispatch", async (t) => {
  const repoRoot = await locateRepoRoot();
  const paymentProtoRoot = await locatePaymentProtoRoot();
  const resolverProtoRoot = await locateResolverProtoRoot();
  if (!repoRoot || !paymentProtoRoot || !resolverProtoRoot) {
    t.diagnostic("skipping: required repo roots not found");
    return;
  }

  const tmpDir = await fs.mkdtemp(path.join(tmpdir(), "daydream-daemon-health-"));
  const payerSock = path.join(tmpDir, "payer.sock");
  const resolverSock = path.join(tmpDir, "resolver.sock");
  const overlayPath = path.join(tmpDir, "overlay.yaml");

  const controlServer = createServer();
  const wss = new WebSocketServer({ server: controlServer });
  await new Promise<void>((resolve) => controlServer.listen(0, "127.0.0.1", () => resolve()));
  const controlAddr = controlServer.address();
  if (!controlAddr || typeof controlAddr === "string") {
    throw new Error("missing websocket address");
  }
  const controlUrl = `ws://127.0.0.1:${controlAddr.port}/control`;

  const brokerCapture: BrokerCapture = { requests: 0 };
  const health: HealthState = {
    status: "degraded",
    staleAfterIso: new Date(Date.now() + 75).toISOString(),
  };
  const broker = await startBroker(brokerCapture, health, controlUrl);
  const payerSrv = await startStubPayerDaemon(payerSock, paymentProtoRoot);

  const overlay = `overlay:
  - eth_address: "0x1111111111111111111111111111111111111111"
    enabled: true
    unsigned_allowed: true
    pin:
      - id: "orch-a"
        url: "${broker.url}"
        capabilities:
          - name: "daydream:scope:v1"
            work_unit: "sessions"
            offerings:
              - id: "default"
                price_per_work_unit_wei: "1"
`;
  await fs.writeFile(overlayPath, overlay, "utf8");

  const { child, stderr } = await startActualResolverDaemon(repoRoot, resolverSock, overlayPath);
  t.after(async () => {
    for (const client of wss.clients) {
      client.close();
    }
    await new Promise<void>((resolve) => wss.close(() => resolve()));
    await new Promise<void>((resolve) => controlServer.close(() => resolve()));
    shutdownPayer();
    await new Promise<void>((resolve) => payerSrv.tryShutdown(() => resolve()));
    await broker.app.close();
    await stopChild(child);
    await fs.rm(tmpDir, { recursive: true, force: true });
  });

  await waitFor(() => fileExists(resolverSock), 20_000, "resolver socket");
  await initPayer({
    socketPath: payerSock,
    protoRoot: paymentProtoRoot,
  });

  const cfg: Config = {
    listen: ":9100",
    payerDaemonSocket: payerSock,
    resolverSocket: resolverSock,
    capabilityId: "daydream:scope:v1",
    offeringId: "default",
    interactionMode: "session-control-external-media@v0",
    resolverSnapshotTtlMs: 1,
    paymentProtoRoot,
    resolverProtoRoot,
    routeFailureThreshold: 1,
    routeCooldownMs: 60_000,
  };
  const selector = createOrchSelector(cfg);
  const router = new SessionRouter();
  const app = Fastify({ logger: false });
  registerOrchRoutes(app, selector);
  registerSessionRoutes(app, cfg, selector, router);
  t.after(async () => {
    for (const session of router.list()) {
      session.controlWs?.end();
    }
    await app.close();
  });

  const first = await app.inject({
    method: "POST",
    url: "/v1/sessions",
  });
  assert.equal(first.statusCode, 503, `expected no orch while live health is red; daemon stderr=${stderr.join("")}`);
  assert.equal(brokerCapture.requests, 0, `broker should not receive session-open while live health is red; daemon stderr=${stderr.join("")}`);
  assert.equal(first.json().error, "no_orchs_available");

  await new Promise((resolve) => setTimeout(resolve, 125));
  health.status = "ready";
  health.staleAfterIso = new Date(Date.now() + 60_000).toISOString();

  const second = await app.inject({
    method: "POST",
    url: "/v1/sessions",
  });
  assert.equal(second.statusCode, 201, `expected session-open after live health turns ready; daemon stderr=${stderr.join("")}`);
  assert.equal(brokerCapture.requests, 1);
  const body = second.json() as { session_id: string; orch: { broker_url: string } };
  assert.equal(body.orch.broker_url, broker.url);
});
