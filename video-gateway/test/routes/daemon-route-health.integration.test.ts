import assert from "node:assert/strict";
import { spawn, type ChildProcessByStdio } from "node:child_process";
import type { Readable } from "node:stream";
import { fileURLToPath } from "node:url";
import path from "node:path";
import fs from "node:fs/promises";
import { tmpdir } from "node:os";
import { test } from "node:test";

import Fastify, { type FastifyInstance } from "fastify";

import type { Config } from "../../src/config.js";
import { createLiveSessionDirectory } from "../../src/livepeer/liveSessionDirectory.js";
import { createRouteSelector } from "../../src/livepeer/routeSelector.js";
import { registerLiveStreams } from "../../src/routes/live-streams.js";
import { createInMemoryLiveStreamRepo } from "../../src/testing/repoFakes.js";

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
    await reply.code(200).header("Content-Type", "application/json").send({
      broker_status: "ready",
      generated_at: new Date().toISOString(),
      capabilities: [
        {
          id: "video:live.rtmp",
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
      rtmp_ingest_url: "rtmp://broker.internal/live/stream-key",
      hls_playback_url: "https://playback.example.com/hls/live/index.m3u8",
      expires_at: "2026-05-15T00:00:00Z",
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

test("video-gateway uses actual resolver daemon live health before live session-open dispatch", async (t) => {
  const repoRoot = await locateRepoRoot();
  const resolverProtoRoot = await locateResolverProtoRoot();
  if (!repoRoot || !resolverProtoRoot) {
    t.diagnostic("skipping: required repo roots not found");
    return;
  }

  const tmpDir = await fs.mkdtemp(path.join(tmpdir(), "video-daemon-health-"));
  const resolverSock = path.join(tmpDir, "resolver.sock");
  const overlayPath = path.join(tmpDir, "overlay.yaml");

  const brokerCapture: BrokerCapture = { requests: 0 };
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
          - name: "video:live.rtmp"
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

  const cfg: Config = {
    brokerUrl: null,
    brokerRtmpHost: "broker.internal",
    resolverSocket: resolverSock,
    resolverProtoRoot,
    resolverSnapshotTtlMs: 1,
    listenPort: 3000,
    rtmpListenAddr: ":1935",
    hlsBaseUrl: "https://playback.example.com",
    payerDaemonSocket: "/var/run/livepeer/payer-daemon.sock",
    databaseUrl: "postgres://video:video@localhost:5432/video",
    redisUrl: "redis://localhost:6379/0",
    vodTusPath: "/v1/uploads",
    webhookHmacPepper: "pepper",
    staleStreamSweepIntervalSec: 60,
    abrPolicy: "customer-tier",
    customerPortalPepper: "pepper",
    adminTokens: [],
    brokerCallTimeoutMs: 30_000,
    routeFailureThreshold: 1,
    routeCooldownMs: 60_000,
  };
  const routeSelector = createRouteSelector({
    brokerUrl: cfg.brokerUrl,
    resolverSocket: cfg.resolverSocket,
    resolverProtoRoot: cfg.resolverProtoRoot,
    resolverSnapshotTtlMs: cfg.resolverSnapshotTtlMs,
    routeFailureThreshold: cfg.routeFailureThreshold,
    routeCooldownMs: cfg.routeCooldownMs,
  });
  const app = Fastify();
  registerLiveStreams(app, {
    cfg,
    routeSelector,
    liveSessions: createLiveSessionDirectory(),
    liveStreamsRepo: createInMemoryLiveStreamRepo(),
  });
  t.after(async () => {
    await app.close();
  });

  const first = await app.inject({
    method: "POST",
    url: "/v1/live/streams",
    payload: {
      project_id: "proj_1",
      name: "No route while unhealthy",
    },
  });
  assert.equal(first.statusCode, 503, `expected no route while live health is red; daemon stderr=${stderr.join("")}`);
  assert.equal(brokerCapture.requests, 0, `broker should not receive session-open while live health is red; daemon stderr=${stderr.join("")}`);
  assert.deepEqual(first.json(), {
    error: "no_live_route",
    message: "no video:live.rtmp route available",
  });

  await new Promise((resolve) => setTimeout(resolve, 125));
  health.status = "ready";
  health.staleAfterIso = new Date(Date.now() + 60_000).toISOString();

  const second = await app.inject({
    method: "POST",
    url: "/v1/live/streams",
    payload: {
      project_id: "proj_1",
      name: "Route recovers",
    },
  });
  assert.equal(second.statusCode, 201, `expected live session-open after live health turns ready; daemon stderr=${stderr.join("")}`);
  assert.equal(brokerCapture.requests, 1);
  const body = second.json() as { session_id: string; rtmp_push_url: string };
  assert.match(body.session_id, /^sess_/);
  assert.equal(body.rtmp_push_url, "rtmp://broker.internal/live/stream-key");
});
