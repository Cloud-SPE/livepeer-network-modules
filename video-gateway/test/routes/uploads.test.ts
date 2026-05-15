import assert from "node:assert/strict";
import { test } from "node:test";

import Fastify from "fastify";

import type { Config } from "../../src/config.js";
import { registerUploads } from "../../src/routes/uploads.js";

const cfg: Config = {
  brokerUrl: "http://broker.internal:8080",
  brokerRtmpHost: "broker.internal",
  resolverSocket: null,
  resolverProtoRoot: "/tmp/proto-contracts",
  resolverSnapshotTtlMs: 15000,
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
  brokerCallTimeoutMs: 30000,
  routeFailureThreshold: 2,
  routeCooldownMs: 30000,
};

test("upload routes: public upload init is rejected when no creator is wired", async () => {
  const app = Fastify();
  registerUploads(app, { cfg });

  const res = await app.inject({
    method: "POST",
    url: "/v1/uploads",
  });
  assert.equal(res.statusCode, 501);
  assert.deepEqual(res.json(), {
    error: "upload_init_unavailable",
    message: "create uploads through the customer portal or a pre-provisioned upload flow",
  });
});

test("upload routes: HEAD and PATCH reject unknown upload ids", async () => {
  const app = Fastify();
  registerUploads(app, {
    cfg,
    uploadExists: async (id: string) => id === "upload_known",
    completeUpload: async (id: string) => id === "upload_known",
  });

  const headMissing = await app.inject({
    method: "HEAD",
    url: "/v1/uploads/upload_missing",
  });
  assert.equal(headMissing.statusCode, 404);

  const patchMissing = await app.inject({
    method: "PATCH",
    url: "/v1/uploads/upload_missing",
  });
  assert.equal(patchMissing.statusCode, 404);
  assert.deepEqual(patchMissing.json(), { error: "upload_not_found" });
});

test("upload routes: HEAD and PATCH succeed for known upload ids", async () => {
  const app = Fastify();
  registerUploads(app, {
    cfg,
    uploadExists: async (id: string) => id === "upload_known",
    completeUpload: async (id: string) => id === "upload_known",
  });

  const headRes = await app.inject({
    method: "HEAD",
    url: "/v1/uploads/upload_known",
  });
  assert.equal(headRes.statusCode, 200);
  assert.equal(headRes.headers["tus-resumable"], "1.0.0");

  const patchRes = await app.inject({
    method: "PATCH",
    url: "/v1/uploads/upload_known",
  });
  assert.equal(patchRes.statusCode, 204);
  assert.equal(patchRes.headers["tus-resumable"], "1.0.0");
});
