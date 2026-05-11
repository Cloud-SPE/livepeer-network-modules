import assert from "node:assert/strict";
import { test } from "node:test";

import Fastify from "fastify";

import type { Config } from "../../src/config.js";
import { createLiveSessionDirectory } from "../../src/livepeer/liveSessionDirectory.js";
import type { VideoRouteSelector } from "../../src/livepeer/routeSelector.js";
import { registerLiveStreams } from "../../src/routes/live-streams.js";
import { createInMemoryLiveStreamRepo } from "../../src/testing/repoFakes.js";

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
};

const routeSelector: VideoRouteSelector = {
  async select() {
    return [];
  },
  async inspect() {
    return [];
  },
};

test("live routes: status and end reflect persisted stream state", async () => {
  const app = Fastify();
  const repo = createInMemoryLiveStreamRepo();
  const liveSessions = createLiveSessionDirectory();
  registerLiveStreams(app, {
    cfg,
    routeSelector,
    liveSessions,
    liveStreamsRepo: repo,
  });

  const createdAt = new Date("2026-05-10T14:00:00Z");
  await repo.insert({
    id: "live_1",
    projectId: "proj_1",
    name: "Launch stream",
    streamKeyHash: "hash",
    status: "active",
    ingestProtocol: "rtmp",
    recordingEnabled: true,
    sessionId: "sess_1",
    workerUrl: "http://broker.internal:8080",
    selectedCapability: "video:live.rtmp",
    selectedOffering: "default",
    createdAt,
    lastSeenAt: createdAt,
  });
  liveSessions.record({
    streamId: "live_1",
    sessionId: "sess_1",
    brokerUrl: "http://broker.internal:8080",
    brokerRtmpUrl: "rtmp://broker.internal/live/stream-key",
    streamKey: "stream-key",
    hlsPlaybackUrl: "https://playback.example.com/hls/live_1/index.m3u8",
  });

  const getRes = await app.inject({
    method: "GET",
    url: "/v1/live/streams/live_1",
  });
  assert.equal(getRes.statusCode, 200);
  assert.deepEqual(getRes.json(), {
    stream_id: "live_1",
    name: "Launch stream",
    project_id: "proj_1",
    status: "live",
    session_id: "sess_1",
    playback_url: "https://playback.example.com/hls/live_1/index.m3u8",
    record_to_vod: true,
    created_at: createdAt.toISOString(),
    ended_at: null,
    cost_accrued_cents: 0,
    billing: null,
  });

  const endRes = await app.inject({
    method: "POST",
    url: "/v1/live/streams/live_1/end",
  });
  assert.equal(endRes.statusCode, 200);
  assert.equal(endRes.json().status, "ended");

  const ended = await repo.byId("live_1");
  assert.equal(ended?.status, "ended");
  assert.ok(ended?.endedAt instanceof Date);
  assert.equal(ended?.lastSeenAt?.getTime(), ended?.endedAt?.getTime());
});

test("live routes: missing stream returns 404", async () => {
  const app = Fastify();
  registerLiveStreams(app, {
    cfg,
    routeSelector,
    liveSessions: createLiveSessionDirectory(),
    liveStreamsRepo: createInMemoryLiveStreamRepo(),
  });

  const res = await app.inject({
    method: "GET",
    url: "/v1/live/streams/missing",
  });
  assert.equal(res.statusCode, 404);
  assert.deepEqual(res.json(), { error: "stream_not_found" });
});

test("live routes: create rejects unknown project when project validation is enabled", async () => {
  const app = Fastify();
  registerLiveStreams(app, {
    cfg,
    routeSelector,
    liveSessions: createLiveSessionDirectory(),
    liveStreamsRepo: createInMemoryLiveStreamRepo(),
    projectExists: async (projectId: string) => projectId === "proj_exists",
  });

  const missing = await app.inject({
    method: "POST",
    url: "/v1/live/streams",
    payload: {
      project_id: "proj_missing",
      name: "Missing project stream",
    },
  });
  assert.equal(missing.statusCode, 404);
  assert.deepEqual(missing.json(), { error: "project_not_found" });
});
