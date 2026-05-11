import assert from "node:assert/strict";
import { test } from "node:test";

import Fastify from "fastify";

import type { VideoRouteSelector } from "../../src/livepeer/routeSelector.js";
import { registerVod } from "../../src/routes/vod.js";
import type { UsageLedger } from "../../src/service/usageLedger.js";
import { createInMemoryAssetRepo } from "../../src/testing/repoFakes.js";

const routeSelector: VideoRouteSelector = {
  async select() {
    return [
      {
        brokerUrl: "http://broker.internal:8080",
        ethAddress: "0x1234",
        capability: "video:transcode.abr",
        offering: "abr-default",
        pricePerWorkUnitWei: "25000000",
        extra: { provider: "abr-runner" },
        constraints: { gpu: "nvidia" },
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
};

test("vod routes: submit persists selected route intent onto the asset", async () => {
  const app = Fastify();
  const repo = createInMemoryAssetRepo();
  await repo.insert({
    id: "asset_1",
    projectId: "proj_1",
    status: "preparing",
    sourceType: "upload",
    sourceUrl: "uploads/proj_1/asset_1/source.mp4",
    encodingTier: "baseline",
    createdAt: new Date("2026-05-10T14:00:00Z"),
  });
  registerVod(app, { routeSelector, assetsRepo: repo });

  const submitRes = await app.inject({
    method: "POST",
    url: "/v1/vod/submit",
    payload: {
      asset_id: "asset_1",
      encoding_tier: "premium",
      offering: "default",
    },
  });
  assert.equal(submitRes.statusCode, 202);
  assert.deepEqual(submitRes.json(), {
    asset_id: "asset_1",
    project_id: "proj_1",
    status: "queued",
    selected_capability: "video:transcode.abr",
    selected_pipeline: "abr_ladder",
    encoding_tier: "premium",
    selected_offering: "abr-default",
    selected_broker_url: "http://broker.internal:8080",
    execution_id: null,
    billing: null,
  });

  const asset = await repo.byId("asset_1");
  assert.equal(asset?.status, "queued");
  assert.equal(asset?.encodingTier, "premium");
  assert.equal(asset?.selectedOffering, "abr-default");
  assert.equal(asset?.errorMessage, undefined);

  const statusRes = await app.inject({
    method: "GET",
    url: "/v1/vod/asset_1",
  });
  assert.equal(statusRes.statusCode, 200);
  assert.equal(statusRes.json().project_id, "proj_1");
  assert.equal(statusRes.json().status, "queued");
  assert.equal(statusRes.json().selected_offering, "abr-default");
  assert.equal(statusRes.json().encoding_tier, "premium");
  assert.equal(statusRes.json().playback_id, null);
  assert.equal(statusRes.json().billing, null);
  assert.deepEqual(statusRes.json().renditions, []);
  assert.deepEqual(statusRes.json().jobs, []);

  const listRes = await app.inject({
    method: "GET",
    url: "/v1/videos/assets",
  });
  assert.equal(listRes.statusCode, 200);
  assert.equal(listRes.json().items.length, 1);
  assert.equal(listRes.json().items[0].id, "asset_1");
  assert.equal(listRes.json().items[0].status, "queued");
  assert.equal(listRes.json().items[0].encoding_tier, "premium");
});

test("vod routes: deleted asset cannot be resubmitted", async () => {
  const app = Fastify();
  const repo = createInMemoryAssetRepo();
  await repo.insert({
    id: "asset_deleted",
    projectId: "proj_1",
    status: "deleted",
    sourceType: "upload",
    encodingTier: "baseline",
    deletedAt: new Date("2026-05-10T14:00:00Z"),
  });
  registerVod(app, { routeSelector, assetsRepo: repo });

  const res = await app.inject({
    method: "POST",
    url: "/v1/vod/submit",
    payload: {
      asset_id: "asset_deleted",
      encoding_tier: "standard",
    },
  });
  assert.equal(res.statusCode, 404);
  assert.deepEqual(res.json(), { error: "asset_not_found" });
});

test("vod routes: execution start failure returns 502 and refunds reserved usage", async () => {
  const app = Fastify();
  const repo = createInMemoryAssetRepo();
  await repo.insert({
    id: "asset_exec_fail",
    projectId: "proj_1",
    status: "preparing",
    sourceType: "upload",
    sourceUrl: "uploads/proj_1/asset_exec_fail/source.mp4",
    encodingTier: "baseline",
    createdAt: new Date("2026-05-10T14:00:00Z"),
  });

  const refunded: string[] = [];
  const usageLedger: UsageLedger = {
    async reserveVodEstimate() {
      return null;
    },
    async recordVodUsage() {
      return 0;
    },
    async refundVodUsage(input) {
      refunded.push(input.assetId);
      return null;
    },
    async recordLiveUsage() {
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

  registerVod(app, {
    routeSelector,
    assetsRepo: repo,
    usageLedger,
    execution: {
      async submitAsset() {
        throw new Error("worker unavailable");
      },
      async retryAsset() {
        return { executionId: "unused" };
      },
      async handoffRecording() {
        return { assetId: "unused", executionId: "unused" };
      },
      async recoverPendingAssets() {
        return [];
      },
    },
  });

  const res = await app.inject({
    method: "POST",
    url: "/v1/vod/submit",
    payload: {
      asset_id: "asset_exec_fail",
      encoding_tier: "standard",
      estimated_duration_sec: 42,
    },
  });
  assert.equal(res.statusCode, 502);
  assert.deepEqual(res.json(), {
    error: "execution_start_failed",
    message: "worker unavailable",
  });
  assert.deepEqual(refunded, ["asset_exec_fail"]);
});
