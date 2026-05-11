import assert from "node:assert/strict";
import { test } from "node:test";

import Fastify from "fastify";

import { createLiveSessionDirectory } from "../../src/livepeer/liveSessionDirectory.js";
import type { VideoRouteSelector } from "../../src/livepeer/routeSelector.js";
import { registerAdmin } from "../../src/routes/admin.js";
import {
  createInMemoryAssetRepo,
  createInMemoryLiveStreamRepo,
  createInMemoryRecordingRepo,
  createInMemoryWebhookFailureRepo,
} from "../../src/testing/repoFakes.js";

const authResolver = {
  async resolve() {
    return { actor: "operator" };
  },
};

const routeSelector: VideoRouteSelector = {
  async select() {
    return [];
  },
  async inspect() {
    return [];
  },
  async suppressBroker() {},
  async unsuppressBroker() {},
  async suppressedBrokers() {
    return [];
  },
};

function liveStreamsDbFake(rows: Array<Record<string, unknown>>) {
  return {
    select() {
      return {
        from() {
          return {
            where() {
              return this;
            },
            orderBy() {
              return this;
            },
            limit() {
              return Promise.resolve(rows);
            },
          };
        },
      };
    },
  };
}

test("admin routes: asset retry requeues non-ready assets", async () => {
  const app = Fastify();
  const assets = createInMemoryAssetRepo();
  await assets.insert({
    id: "asset_1",
    projectId: "proj_1",
    status: "errored",
    sourceType: "upload",
    encodingTier: "standard",
    createdAt: new Date("2026-05-11T12:00:00Z"),
  });
  const retried: string[] = [];
  registerAdmin(app, {
    authResolver,
    videoDb: {} as never,
    routeSelector,
    liveSessions: createLiveSessionDirectory(),
    assets,
    liveStreamsRepo: createInMemoryLiveStreamRepo(),
    recordingsRepo: createInMemoryRecordingRepo(),
    failures: createInMemoryWebhookFailureRepo(),
    dispatcher: {
      async dispatch() {
        return { delivered: true, attempts: 1, finalStatus: 200, lastError: null };
      },
      async replayFailure() {
        return { delivered: true, attempts: 1, finalStatus: 200, lastError: null };
      },
    },
    execution: {
      async submitAsset() {
        return { executionId: "job_unused" };
      },
      async retryAsset(assetId: string) {
        retried.push(assetId);
        return { executionId: "job_retry_1" };
      },
      async handoffRecording() {
        return { assetId: "asset_recording", executionId: "job_recording" };
      },
    },
  });

  const res = await app.inject({
    method: "POST",
    url: "/admin/assets/asset_1/retry",
    headers: {
      authorization: "Bearer token",
      "x-actor": "operator",
    },
  });
  assert.equal(res.statusCode, 202);
  assert.deepEqual(res.json(), {
    asset_id: "asset_1",
    status: "queued",
    execution_id: "job_retry_1",
  });
  assert.deepEqual(retried, ["asset_1"]);
});

test("admin routes: ready assets cannot be retried", async () => {
  const app = Fastify();
  const assets = createInMemoryAssetRepo();
  await assets.insert({
    id: "asset_ready",
    projectId: "proj_1",
    status: "ready",
    sourceType: "upload",
    encodingTier: "standard",
    createdAt: new Date("2026-05-11T12:00:00Z"),
  });
  registerAdmin(app, {
    authResolver,
    videoDb: {} as never,
    routeSelector,
    liveSessions: createLiveSessionDirectory(),
    assets,
    liveStreamsRepo: createInMemoryLiveStreamRepo(),
    recordingsRepo: createInMemoryRecordingRepo(),
    failures: createInMemoryWebhookFailureRepo(),
    dispatcher: {
      async dispatch() {
        return { delivered: true, attempts: 1, finalStatus: 200, lastError: null };
      },
      async replayFailure() {
        return { delivered: true, attempts: 1, finalStatus: 200, lastError: null };
      },
    },
    execution: {
      async submitAsset() {
        return { executionId: "job_unused" };
      },
      async retryAsset() {
        return { executionId: "job_retry_1" };
      },
      async handoffRecording() {
        return { assetId: "asset_recording", executionId: "job_recording" };
      },
    },
  });

  const res = await app.inject({
    method: "POST",
    url: "/admin/assets/asset_ready/retry",
    headers: {
      authorization: "Bearer token",
      "x-actor": "operator",
    },
  });
  assert.equal(res.statusCode, 409);
  assert.deepEqual(res.json(), { error: "asset_already_ready" });
});

test("admin routes: recording retry requeues failed recording assets", async () => {
  const app = Fastify();
  const assets = createInMemoryAssetRepo();
  const recordings = createInMemoryRecordingRepo();
  await assets.insert({
    id: "asset_failed",
    projectId: "proj_1",
    status: "errored",
    sourceType: "live_recording",
    encodingTier: "standard",
    createdAt: new Date("2026-05-11T12:00:00Z"),
  });
  await recordings.insert({
    id: "rec_1",
    liveStreamId: "live_1",
    assetId: "asset_failed",
    status: "failed",
    startedAt: new Date("2026-05-11T12:00:00Z"),
    endedAt: new Date("2026-05-11T12:05:00Z"),
  });
  const retried: string[] = [];
  registerAdmin(app, {
    authResolver,
    videoDb: {} as never,
    routeSelector,
    liveSessions: createLiveSessionDirectory(),
    assets,
    liveStreamsRepo: createInMemoryLiveStreamRepo(),
    recordingsRepo: recordings,
    failures: createInMemoryWebhookFailureRepo(),
    dispatcher: {
      async dispatch() {
        return { delivered: true, attempts: 1, finalStatus: 200, lastError: null };
      },
      async replayFailure() {
        return { delivered: true, attempts: 1, finalStatus: 200, lastError: null };
      },
    },
    execution: {
      async submitAsset() {
        return { executionId: "job_unused" };
      },
      async retryAsset(assetId: string) {
        retried.push(assetId);
        return { executionId: "job_retry_recording" };
      },
      async handoffRecording() {
        return { assetId: "asset_recording", executionId: "job_recording" };
      },
    },
  });

  const res = await app.inject({
    method: "POST",
    url: "/admin/recordings/rec_1/retry",
    headers: {
      authorization: "Bearer token",
      "x-actor": "operator",
    },
  });
  assert.equal(res.statusCode, 202);
  assert.deepEqual(res.json(), {
    recording_id: "rec_1",
    asset_id: "asset_failed",
    status: "pending",
    execution_id: "job_retry_recording",
  });
  assert.deepEqual(retried, ["asset_failed"]);
  const updated = await recordings.byId("rec_1");
  assert.equal(updated?.status, "pending");
});

test("admin routes: broker suppression toggles route controls", async () => {
  const app = Fastify();
  const suppressed = new Set<string>();
  const controllableSelector: VideoRouteSelector = {
    async select() {
      return [];
    },
    async inspect() {
      return [
        {
          brokerUrl: "http://broker.internal:8080",
          ethAddress: "0x1234",
          capability: "video:transcode.abr",
          offering: "default",
          pricePerWorkUnitWei: "1",
          extra: null,
          constraints: null,
        },
      ];
    },
    async suppressBroker(brokerUrl: string) {
      suppressed.add(brokerUrl);
    },
    async unsuppressBroker(brokerUrl: string) {
      suppressed.delete(brokerUrl);
    },
    async suppressedBrokers() {
      return [...suppressed];
    },
  };
  registerAdmin(app, {
    authResolver,
    videoDb: {} as never,
    routeSelector: controllableSelector,
    liveSessions: createLiveSessionDirectory(),
    assets: createInMemoryAssetRepo(),
    liveStreamsRepo: createInMemoryLiveStreamRepo(),
    recordingsRepo: createInMemoryRecordingRepo(),
    failures: createInMemoryWebhookFailureRepo(),
    dispatcher: {
      async dispatch() {
        return { delivered: true, attempts: 1, finalStatus: 200, lastError: null };
      },
      async replayFailure() {
        return { delivered: true, attempts: 1, finalStatus: 200, lastError: null };
      },
    },
  });

  const suppress = await app.inject({
    method: "POST",
    url: "/admin/video/route-controls/suppress",
    headers: { authorization: "Bearer token", "x-actor": "operator" },
    payload: { broker_url: "http://broker.internal:8080" },
  });
  assert.equal(suppress.statusCode, 200);
  assert.deepEqual(suppress.json(), {
    broker_url: "http://broker.internal:8080",
    suppressed: true,
  });

  const controls = await app.inject({
    method: "GET",
    url: "/admin/video/route-controls",
    headers: { authorization: "Bearer token", "x-actor": "operator" },
  });
  assert.equal(controls.statusCode, 200);
  assert.deepEqual(controls.json(), {
    suppressed_brokers: ["http://broker.internal:8080"],
  });

  const candidates = await app.inject({
    method: "GET",
    url: "/admin/video/resolver-candidates",
    headers: { authorization: "Bearer token", "x-actor": "operator" },
  });
  assert.equal(candidates.statusCode, 200);
  assert.equal(candidates.json().candidates[0].suppressed, true);

  const unsuppress = await app.inject({
    method: "POST",
    url: "/admin/video/route-controls/unsuppress",
    headers: { authorization: "Bearer token", "x-actor": "operator" },
    payload: { broker_url: "http://broker.internal:8080" },
  });
  assert.equal(unsuppress.statusCode, 200);
  assert.deepEqual(unsuppress.json(), {
    broker_url: "http://broker.internal:8080",
    suppressed: false,
  });
});

test("admin routes: live stream list includes health telemetry", async () => {
  const app = Fastify();
  const liveSessions = createLiveSessionDirectory();
  liveSessions.record({
    streamId: "live_healthy",
    sessionId: "sess_1",
    brokerUrl: "http://broker.internal:8080",
    brokerRtmpUrl: "rtmp://broker/live/key",
    streamKey: "key",
    hlsPlaybackUrl: "https://playback.example.com/live.m3u8",
  });
  const now = Date.now();
  registerAdmin(app, {
    authResolver,
    videoDb: liveStreamsDbFake([
      {
        id: "live_healthy",
        name: "Healthy stream",
        projectId: "proj_1",
        status: "active",
        sessionId: "sess_1",
        workerUrl: "http://broker.internal:8080",
        lastSeenAt: new Date(now - 5_000),
        createdAt: new Date(now - 120_000),
        endedAt: null,
        recordingEnabled: true,
      },
      {
        id: "live_stale",
        name: "Stale stream",
        projectId: "proj_1",
        status: "active",
        sessionId: "sess_missing",
        workerUrl: "http://broker.internal:8080",
        lastSeenAt: new Date(now - 120_000),
        createdAt: new Date(now - 180_000),
        endedAt: null,
        recordingEnabled: false,
      },
    ]) as never,
    routeSelector,
    liveSessions,
    assets: createInMemoryAssetRepo(),
    liveStreamsRepo: createInMemoryLiveStreamRepo(),
    recordingsRepo: createInMemoryRecordingRepo(),
    failures: createInMemoryWebhookFailureRepo(),
    dispatcher: {
      async dispatch() {
        return { delivered: true, attempts: 1, finalStatus: 200, lastError: null };
      },
      async replayFailure() {
        return { delivered: true, attempts: 1, finalStatus: 200, lastError: null };
      },
    },
  });

  const res = await app.inject({
    method: "GET",
    url: "/admin/live-streams",
    headers: { authorization: "Bearer token", "x-actor": "operator" },
  });
  assert.equal(res.statusCode, 200);
  const items = res.json().items;
  assert.equal(items[0].health, "healthy");
  assert.equal(items[0].sessionKnown, true);
  assert.equal(items[0].brokerUrl, "http://broker.internal:8080");
  assert.equal(items[1].health, "degraded");
  assert.equal(items[1].sessionKnown, false);
});
