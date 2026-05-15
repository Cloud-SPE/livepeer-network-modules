import assert from "node:assert/strict";
import { test } from "node:test";

import Fastify from "fastify";

import type { Config } from "../../src/config.js";
import { createLiveSessionDirectory } from "../../src/livepeer/liveSessionDirectory.js";
import { RouteHealthTracker } from "../../src/livepeer/routeHealth.js";
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
  routeFailureThreshold: 2,
  routeCooldownMs: 30000,
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
  async recordOutcome() {},
  async inspectHealth() {
    return [];
  },
  async inspectMetrics() {
    return {
      attemptsTotal: 0,
      successesTotal: 0,
      retryableFailuresTotal: 0,
      nonRetryableFailuresTotal: 0,
      cooldownsOpenedTotal: 0,
    };
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

test("live routes: create returns 503 when no live route is available", async () => {
  const app = Fastify();
  registerLiveStreams(app, {
    cfg,
    routeSelector,
    liveSessions: createLiveSessionDirectory(),
    liveStreamsRepo: createInMemoryLiveStreamRepo(),
  });

  const res = await app.inject({
    method: "POST",
    url: "/v1/live/streams",
    payload: {
      project_id: "proj_1",
      name: "No route stream",
    },
  });
  assert.equal(res.statusCode, 503);
  assert.deepEqual(res.json(), {
    error: "no_live_route",
    message: "no video:live.rtmp route available",
  });
});

test("live routes: create returns 502 when broker session-open fails", async () => {
  const app = Fastify();
  const fetchBefore = globalThis.fetch;
  globalThis.fetch = (async () =>
    new Response("upstream down", {
      status: 502,
      headers: { "Content-Type": "text/plain" },
    })) as typeof fetch;
  try {
    registerLiveStreams(app, {
      cfg,
      routeSelector: {
        async select() {
          return [
            {
              brokerUrl: "http://broker.internal:8080",
              ethAddress: "0x1234",
              capability: "video:live.rtmp",
              offering: "default",
              pricePerWorkUnitWei: "25000000",
              extra: null,
              constraints: null,
            },
          ];
        },
        async inspect() {
          return [];
        },
        async suppressBroker() {},
        async unsuppressBroker() {},
        async suppressedBrokers() {
          return [];
        },
        async recordOutcome() {},
        async inspectHealth() {
          return [];
        },
        async inspectMetrics() {
          return {
            attemptsTotal: 0,
            successesTotal: 0,
            retryableFailuresTotal: 0,
            nonRetryableFailuresTotal: 0,
            cooldownsOpenedTotal: 0,
          };
        },
      },
      liveSessions: createLiveSessionDirectory(),
      liveStreamsRepo: createInMemoryLiveStreamRepo(),
    });

    const res = await app.inject({
      method: "POST",
      url: "/v1/live/streams",
      payload: {
        project_id: "proj_1",
        name: "Broker failure stream",
      },
    });
    assert.equal(res.statusCode, 502);
    assert.deepEqual(res.json(), {
      error: "live_session_open_failed",
      message: "live session-open failed: 502",
    });
  } finally {
    globalThis.fetch = fetchBefore;
  }
});

test("live routes: session-open retries next broker and cools failing route for later requests", async () => {
  const app = Fastify();
  const tracker = new RouteHealthTracker({ failureThreshold: 1, cooldownMs: 60_000 });
  const baseRoutes = [
    {
      brokerUrl: "http://broker-a.internal:8080",
      ethAddress: "0xaaaa",
      capability: "video:live.rtmp",
      offering: "default",
      pricePerWorkUnitWei: "1",
      extra: null,
      constraints: null,
    },
    {
      brokerUrl: "http://broker-b.internal:8080",
      ethAddress: "0xbbbb",
      capability: "video:live.rtmp",
      offering: "default",
      pricePerWorkUnitWei: "2",
      extra: null,
      constraints: null,
    },
  ] as const;
  let brokerACalls = 0;
  let brokerBCalls = 0;
  const fetchBefore = globalThis.fetch;
  globalThis.fetch = (async (input) => {
    const url = String(input);
    if (url.startsWith("http://broker-a.internal:8080")) {
      brokerACalls += 1;
      return new Response("upstream down", { status: 503 });
    }
    brokerBCalls += 1;
    return new Response(JSON.stringify({
      session_id: `sess_${brokerBCalls}`,
      rtmp_ingest_url: "rtmp://broker-b.internal/live/stream-key",
      hls_playback_url: "https://playback.example.com/hls/live/index.m3u8",
      expires_at: "2026-05-15T00:00:00Z",
    }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;
  try {
    registerLiveStreams(app, {
      cfg,
      routeSelector: {
        async select() {
          return tracker.rankCandidates([...baseRoutes]);
        },
        async inspect() {
          return [...baseRoutes];
        },
        async suppressBroker() {},
        async unsuppressBroker() {},
        async suppressedBrokers() {
          return [];
        },
        async recordOutcome(candidate, outcome, reason) {
          tracker.record(candidate, outcome, reason);
        },
        async inspectHealth() {
          return tracker.inspect();
        },
        async inspectMetrics() {
          return tracker.inspectMetrics();
        },
      },
      liveSessions: createLiveSessionDirectory(),
      liveStreamsRepo: createInMemoryLiveStreamRepo(),
    });

    const first = await app.inject({
      method: "POST",
      url: "/v1/live/streams",
      payload: {
        project_id: "proj_1",
        name: "Retry stream",
      },
    });
    assert.equal(first.statusCode, 201);
    assert.equal(brokerACalls, 1);
    assert.equal(brokerBCalls, 1);

    const second = await app.inject({
      method: "POST",
      url: "/v1/live/streams",
      payload: {
        project_id: "proj_1",
        name: "Cooldown stream",
      },
    });
    assert.equal(second.statusCode, 201);
    assert.equal(brokerACalls, 1, "cooled broker should be skipped on subsequent request");
    assert.equal(brokerBCalls, 2);
  } finally {
    globalThis.fetch = fetchBefore;
  }
});
