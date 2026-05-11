import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { basename } from "node:path";
import { test } from "node:test";

import {
  resolveCustomerPortalMigrationsDir,
  sweepStaleStreamsOnce,
  resolveVideoGatewayMigrationsDir,
} from "../src/index.js";
import { createLiveSessionDirectory } from "../src/livepeer/liveSessionDirectory.js";
import type { UsageLedger } from "../src/service/usageLedger.js";
import {
  createInMemoryLiveStreamRepo,
  createInMemoryRecordingRepo,
} from "../src/testing/repoFakes.js";

test("resolveCustomerPortalMigrationsDir finds the shared portal migrations", () => {
  const dir = resolveCustomerPortalMigrationsDir();
  assert.ok(existsSync(dir));
  assert.equal(basename(dir), "migrations");
});

test("resolveVideoGatewayMigrationsDir finds the local gateway migrations", () => {
  const dir = resolveVideoGatewayMigrationsDir();
  assert.ok(existsSync(dir));
  assert.equal(basename(dir), "migrations");
});

test("sweepStaleStreamsOnce ends stale live streams and triggers recording handoff", async () => {
  const liveStreams = createInMemoryLiveStreamRepo();
  const recordings = createInMemoryRecordingRepo();
  const liveSessions = createLiveSessionDirectory();
  const now = new Date("2026-05-11T15:00:00Z");
  const createdAt = new Date("2026-05-11T14:55:00Z");
  const staleLastSeen = new Date("2026-05-11T14:58:00Z");

  await liveStreams.insert({
    id: "live_1",
    projectId: "proj_1",
    name: "Stale stream",
    streamKeyHash: "hash",
    status: "active",
    ingestProtocol: "rtmp",
    recordingEnabled: true,
    sessionId: "sess_1",
    workerUrl: "http://broker.internal:8080",
    selectedCapability: "video:live.rtmp",
    selectedOffering: "default",
    createdAt,
    lastSeenAt: staleLastSeen,
  });
  await recordings.insert({
    id: "rec_1",
    liveStreamId: "live_1",
    assetId: null,
    status: "running",
    startedAt: createdAt,
    endedAt: null,
  });
  liveSessions.record({
    streamId: "live_1",
    sessionId: "sess_1",
    brokerUrl: "http://broker.internal:8080",
    brokerRtmpUrl: "rtmp://broker.internal/live/key",
    streamKey: "key",
    hlsPlaybackUrl: "https://playback.example.com/live_1/index.m3u8",
  });

  const usageCalls: Array<{ projectId: string; liveStreamId: string; durationSec: number }> = [];
  const handoffCalls: Array<{ liveStreamId: string; recordingId: string; projectId: string; sourceUrl: string }> = [];

  const usageLedger: UsageLedger = {
    async reserveVodEstimate() {
      return null;
    },
    async recordVodUsage() {
      return 0;
    },
    async refundVodUsage() {
      return null;
    },
    async recordLiveUsage(input) {
      usageCalls.push(input);
      return 0;
    },
    async getChargeByAsset() {
      return null;
    },
    async getChargeByLiveStream() {
      return null;
    },
    async listChargesByWorkIds() {
      return new Map();
    },
    async summarizeCustomer() {
      return {
        topupTotalCents: 0,
        usageCommittedCents: 0,
        reservedOpenCents: 0,
        refundedCents: 0,
      };
    },
  };

  const swept = await sweepStaleStreamsOnce({
    liveStreams,
    liveSessions,
    recordings,
    execution: {
      async submitAsset() {
        return { executionId: "unused" };
      },
      async retryAsset() {
        return { executionId: "unused" };
      },
      async handoffRecording(input) {
        handoffCalls.push({
          liveStreamId: input.liveStreamId,
          recordingId: input.recordingId,
          projectId: input.projectId,
          sourceUrl: input.sourceUrl,
        });
        return { assetId: "asset_1", executionId: "job_1" };
      },
    },
    usageLedger,
    now,
    staleAfterSec: 60,
    logger: console,
  });

  assert.equal(swept, 1);
  const stream = await liveStreams.byId("live_1");
  assert.equal(stream?.status, "ended");
  assert.equal(stream?.endedAt?.toISOString(), now.toISOString());
  assert.deepEqual(usageCalls, [
    { projectId: "proj_1", liveStreamId: "live_1", durationSec: 300 },
  ]);
  assert.deepEqual(handoffCalls, [
    {
      liveStreamId: "live_1",
      recordingId: "rec_1",
      projectId: "proj_1",
      sourceUrl: "https://playback.example.com/live_1/index.m3u8",
    },
  ]);
});
