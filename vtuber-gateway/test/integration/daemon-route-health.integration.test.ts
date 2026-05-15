import test from "node:test";
import assert from "node:assert/strict";
import { spawn, type ChildProcessByStdio } from "node:child_process";
import type { Readable } from "node:stream";
import { fileURLToPath } from "node:url";
import path from "node:path";
import fs from "node:fs/promises";
import { tmpdir } from "node:os";

import Fastify, { type FastifyInstance } from "fastify";

import { ConfigSchema } from "../../src/config.js";
import { createServiceRegistryClient } from "../../src/providers/serviceRegistry.js";
import { buildServer } from "../../src/server.js";
import { createInMemorySessionStore } from "../../src/service/sessions/inMemorySessionStore.js";
import type { VtuberGatewayDeps } from "../../src/runtime/deps.js";

interface BrokerCapture {
  healthRequests: number;
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
  app.get("/registry/health", async (_req, reply) => {
    capture.healthRequests += 1;
    await reply.code(200).header("Content-Type", "application/json").send({
      broker_status: "ready",
      generated_at: new Date().toISOString(),
      capabilities: [
        {
          id: "livepeer:vtuber-session",
          offering_id: "default",
          status: health.status,
          stale_after: health.staleAfterIso,
        },
      ],
    });
  });
  await app.listen({ host: "127.0.0.1", port: 0 });
  const addr = app.server.address();
  if (!addr || typeof addr === "string") throw new Error("no listen address");
  return { app, url: `http://127.0.0.1:${addr.port}` };
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

test("vtuber-gateway uses actual resolver daemon live health before worker start", async (t) => {
  const repoRoot = await locateRepoRoot();
  const resolverProtoRoot = await locateResolverProtoRoot();
  if (!repoRoot || !resolverProtoRoot) {
    t.diagnostic("skipping: required repo roots not found");
    return;
  }

  const tmpDir = await fs.mkdtemp(path.join(tmpdir(), "vtuber-daemon-health-"));
  const resolverSock = path.join(tmpDir, "resolver.sock");
  const overlayPath = path.join(tmpDir, "overlay.yaml");

  const brokerCapture: BrokerCapture = { healthRequests: 0 };
  const health: HealthState = {
    status: "degraded",
    staleAfterIso: new Date(Date.now() + 75).toISOString(),
  };
  const broker = await startBroker(brokerCapture, health);

  const overlay = `overlay:
  - eth_address: "0x1111111111111111111111111111111111111111"
    enabled: true
    unsigned_allowed: true
    pin:
      - id: "orch-a"
        url: "${broker.url}"
        capabilities:
          - name: "livepeer:vtuber-session"
            work_unit: "seconds"
            offerings:
              - id: "default"
                price_per_work_unit_wei: "1"
`;
  await fs.writeFile(overlayPath, overlay, "utf8");

  const { child, stderr } = await startActualResolverDaemon(repoRoot, resolverSock, overlayPath);
  t.after(async () => {
    await broker.app.close();
    await stopChild(child);
    await fs.rm(tmpDir, { recursive: true, force: true });
  });

  await waitFor(() => fileExists(resolverSock), 20_000, "resolver socket");

  const serviceRegistry = createServiceRegistryClient({
    brokerUrl: null,
    resolverSocket: resolverSock,
    resolverProtoRoot,
    resolverSnapshotTtlMs: 1,
    routeFailureThreshold: 1,
    routeCooldownMs: 60_000,
  });
  t.after(async () => {
    await serviceRegistry.close();
  });

  const workerCalls: string[] = [];
  const deps: VtuberGatewayDeps = {
    cfg: ConfigSchema.parse({
      listenPort: 3001,
      logLevel: "fatal",
      brokerUrl: null,
      resolverSocket: resolverSock,
      resolverProtoRoot,
      resolverSnapshotTtlMs: 1,
      paymentProtoRoot: "/tmp/proto",
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
      databaseUrl: "postgres://localhost/vtuber_gateway",
      customerPortalPepper: "dev-pepper",
      routeFailureThreshold: 1,
      routeCooldownMs: 60_000,
    }),
    sessionStore: createInMemorySessionStore(),
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
        return {
          payerWorkId: "work-1",
          paymentHeader: "lp-payment-header-stub",
        };
      },
      async close() {},
    },
    serviceRegistry,
    worker: {
      async startSession(nodeUrl) {
        workerCalls.push(nodeUrl);
        return {
          session_id: "worker-sess-1",
          status: "active",
          started_at: new Date().toISOString(),
          control_url: "ws://gw.invalid/control",
          expires_at: new Date(Date.now() + 60_000).toISOString(),
        };
      },
      async stopSession() {},
      async topupSession() {},
    },
  };

  const app = await buildServer(deps);
  t.after(async () => {
    await app.close();
  });

  const payload = {
    persona: "grifter",
    vrm_url: "https://example.com/avatar.vrm",
    llm_provider: "livepeer",
    tts_provider: "livepeer",
  };

  const first = await app.inject({
    method: "POST",
    url: "/v1/vtuber/sessions",
    headers: {
      "content-type": "application/json",
      authorization: "Bearer sk-test",
    },
    payload,
  });
  assert.equal(first.statusCode, 503, `expected no worker while live health is red; daemon stderr=${stderr.join("")}`);
  assert.equal(first.headers["retry-after"], "5");
  assert.equal(first.headers["livepeer-error"], "no_worker_available");
  assert.deepEqual(first.json(), { error: "no_worker_available" });
  assert.deepEqual(workerCalls, [], `worker should not be called while live health is red; daemon stderr=${stderr.join("")}`);

  await new Promise((resolve) => setTimeout(resolve, 125));
  health.status = "ready";
  health.staleAfterIso = new Date(Date.now() + 60_000).toISOString();

  const second = await app.inject({
    method: "POST",
    url: "/v1/vtuber/sessions",
    headers: {
      "content-type": "application/json",
      authorization: "Bearer sk-test",
    },
    payload,
  });
  assert.equal(second.statusCode, 200, `expected worker start after live health turns ready; daemon stderr=${stderr.join("")}`);
  assert.deepEqual(workerCalls, [broker.url]);
  const body = second.json() as { session_id: string; control_url: string };
  assert.match(body.session_id, /[0-9a-f-]{36}/);
  assert.equal(body.control_url, "ws://gw.invalid/control");
});
