import assert from "node:assert/strict";
import { test } from "node:test";

import type { VideoRouteCandidate, VideoRouteSelector } from "../../src/livepeer/routeSelector.js";
import { createAbrExecutionManager } from "../../src/service/abrExecution.js";
import {
  createInMemoryAssetRepo,
  createInMemoryEncodingJobRepo,
  createInMemoryLiveStreamRepo,
  createInMemoryPlaybackIdRepo,
  createInMemoryRecordingRepo,
  createInMemoryRenditionRepo,
} from "../../src/testing/repoFakes.js";
import type { StorageProvider } from "../../src/engine/interfaces/storageProvider.js";

function createStorageFake(): StorageProvider {
  return {
    async putSignedUploadUrl(opts) {
      const storageKey = `${opts.kind}/${opts.assetId}/${opts.filename}`;
      return {
        url: `https://storage.invalid/upload/${encodeURIComponent(storageKey)}`,
        storageKey,
      };
    },
    async getSignedDownloadUrl(opts) {
      return `https://storage.invalid/download/${encodeURIComponent(opts.storageKey)}`;
    },
    async putObject() {},
    async delete() {},
    async copyObject() {},
    pathFor(opts) {
      return `${opts.kind}/${opts.assetId ?? opts.streamId ?? "root"}/${opts.filename}`;
    },
  };
}

const route: VideoRouteCandidate = {
  brokerUrl: "http://worker.internal:8080",
  ethAddress: "0x1234",
  capability: "video:transcode.abr",
  offering: "abr-default",
  pricePerWorkUnitWei: "25000000",
  extra: { runner_url: "http://worker.internal:8080" },
  constraints: null,
};

function createRouteSelectorFake(): VideoRouteSelector {
  return {
    async select() {
      return [route];
    },
    async inspect() {
      return [route];
    },
    async suppressBroker() {},
    async unsuppressBroker() {},
    async suppressedBrokers() {
      return [];
    },
  };
}

test("ABR execution manager: submitAsset drives asset to ready with renditions and playback", async () => {
  const assets = createInMemoryAssetRepo();
  const jobs = createInMemoryEncodingJobRepo();
  const renditions = createInMemoryRenditionRepo();
  const playbackIds = createInMemoryPlaybackIdRepo();
  const recordings = createInMemoryRecordingRepo();
  const liveStreams = createInMemoryLiveStreamRepo();

  await assets.insert({
    id: "asset_1",
    projectId: "proj_1",
    status: "queued",
    sourceType: "upload",
    sourceUrl: "uploads/proj_1/asset_1/source.mp4",
    encodingTier: "standard",
    createdAt: new Date("2026-05-10T15:00:00Z"),
  });

  let statusCalls = 0;
  const manager = createAbrExecutionManager({
    assets,
    jobs,
    renditions,
    playbackIds,
    recordings,
    liveStreams,
    storage: createStorageFake(),
    routeSelector: createRouteSelectorFake(),
    fetchImpl: async (url, init) => {
      if (url.endsWith("/v1/video/transcode/abr")) {
        return new Response(JSON.stringify({ job_id: "native_1" }), {
          status: 202,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (url.endsWith("/v1/video/transcode/abr/status")) {
        statusCalls += 1;
        return new Response(
          JSON.stringify({
            job_id: "native_1",
            status: "complete",
            input: {
              duration: 123.5,
              width: 1920,
              height: 1080,
              fps: 30,
              video_codec: "h264",
              audio_codec: "aac",
            },
            renditions: [
              { name: "1080p", status: "complete", bitrate: 5000 },
              { name: "720p", status: "complete", bitrate: 2500 },
              { name: "480p", status: "complete", bitrate: 1000 },
              { name: "360p", status: "complete", bitrate: 600 },
            ],
          }),
          {
            status: 200,
            headers: { "Content-Type": "application/json" },
          },
        );
      }
      throw new Error(`unexpected url ${url} ${init?.method ?? "GET"}`);
    },
  });

  const out = await manager.submitAsset({ assetId: "asset_1", route });
  assert.match(out.executionId, /^job_/);
  await new Promise((resolve) => setTimeout(resolve, 0));

  const asset = await assets.byId("asset_1");
  assert.equal(asset?.status, "ready");
  assert.equal(asset?.readyAt instanceof Date, true);
  assert.equal(asset?.durationSec, 123.5);
  assert.equal(asset?.videoCodec, "h264");

  const assetRenditions = await renditions.byAsset("asset_1");
  assert.equal(assetRenditions.length, 4);
  assert.equal(assetRenditions.every((row) => row.status === "completed"), true);

  const assetJobs = await jobs.byAsset("asset_1");
  assert.equal(assetJobs.length, 1);
  assert.equal(assetJobs[0]?.status, "completed");

  const playback = await playbackIds.byAsset("asset_1");
  assert.equal(playback.length, 1);
  assert.equal(playback[0]?.id, "play_asset_1");
  assert.equal(statusCalls >= 1, true);
});

test("ABR execution manager: submitAsset failure persists errored state", async () => {
  const assets = createInMemoryAssetRepo();
  const jobs = createInMemoryEncodingJobRepo();
  const renditions = createInMemoryRenditionRepo();
  const playbackIds = createInMemoryPlaybackIdRepo();
  const recordings = createInMemoryRecordingRepo();
  const liveStreams = createInMemoryLiveStreamRepo();

  await assets.insert({
    id: "asset_fail",
    projectId: "proj_1",
    status: "queued",
    sourceType: "upload",
    sourceUrl: "uploads/proj_1/asset_fail/source.mp4",
    encodingTier: "baseline",
  });

  const manager = createAbrExecutionManager({
    assets,
    jobs,
    renditions,
    playbackIds,
    recordings,
    liveStreams,
    storage: createStorageFake(),
    routeSelector: createRouteSelectorFake(),
    fetchImpl: async (url) => {
      if (url.endsWith("/v1/video/transcode/abr")) {
        return new Response(JSON.stringify({ job_id: "native_fail" }), {
          status: 202,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (url.endsWith("/v1/video/transcode/abr/status")) {
        return new Response(
          JSON.stringify({
            job_id: "native_fail",
            status: "error",
            error: "encoder crashed",
            error_code: "encoder_failed",
          }),
          {
            status: 200,
            headers: { "Content-Type": "application/json" },
          },
        );
      }
      throw new Error(`unexpected url ${url}`);
    },
  });

  await manager.submitAsset({ assetId: "asset_fail", route });
  await new Promise((resolve) => setTimeout(resolve, 0));

  const asset = await assets.byId("asset_fail");
  assert.equal(asset?.status, "errored");
  assert.match(asset?.errorMessage ?? "", /encoder_failed/);

  const assetJobs = await jobs.byAsset("asset_fail");
  assert.equal(assetJobs[0]?.status, "failed");
});

test("ABR execution manager: handoffRecording creates linked VOD asset", async () => {
  const assets = createInMemoryAssetRepo();
  const jobs = createInMemoryEncodingJobRepo();
  const renditions = createInMemoryRenditionRepo();
  const playbackIds = createInMemoryPlaybackIdRepo();
  const recordings = createInMemoryRecordingRepo();
  const liveStreams = createInMemoryLiveStreamRepo();

  await liveStreams.insert({
    id: "live_1",
    projectId: "proj_1",
    streamKeyHash: "hash_1",
    status: "ended",
    ingestProtocol: "rtmp",
    recordingEnabled: true,
  });
  await recordings.insert({
    id: "rec_1",
    liveStreamId: "live_1",
    assetId: null,
    status: "pending",
    startedAt: new Date("2026-05-10T15:00:00Z"),
    endedAt: new Date("2026-05-10T15:05:00Z"),
  });

  const manager = createAbrExecutionManager({
    assets,
    jobs,
    renditions,
    playbackIds,
    recordings,
    liveStreams,
    storage: createStorageFake(),
    routeSelector: createRouteSelectorFake(),
    fetchImpl: async (url) => {
      if (url.endsWith("/v1/video/transcode/abr")) {
        return new Response(JSON.stringify({ job_id: "native_recording" }), {
          status: 202,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (url.endsWith("/v1/video/transcode/abr/status")) {
        return new Response(
          JSON.stringify({
            job_id: "native_recording",
            status: "complete",
            input: {
              duration: 300,
              width: 1920,
              height: 1080,
              fps: 30,
              video_codec: "h264",
              audio_codec: "aac",
            },
            renditions: [
              { name: "1080p", status: "complete", bitrate: 5000 },
              { name: "720p", status: "complete", bitrate: 2500 },
              { name: "480p", status: "complete", bitrate: 1000 },
              { name: "360p", status: "complete", bitrate: 600 },
            ],
          }),
          {
            status: 200,
            headers: { "Content-Type": "application/json" },
          },
        );
      }
      throw new Error(`unexpected url ${url}`);
    },
  });

  const out = await manager.handoffRecording({
    liveStreamId: "live_1",
    recordingId: "rec_1",
    projectId: "proj_1",
    sourceUrl: "https://playback.invalid/live_1/master.m3u8",
    endedAt: new Date("2026-05-10T15:05:00Z"),
  });
  await new Promise((resolve) => setTimeout(resolve, 0));

  const recording = await recordings.byId("rec_1");
  assert.equal(recording?.status, "ready");
  assert.equal(recording?.assetId, out.assetId);

  const asset = await assets.byId(out.assetId);
  assert.equal(asset?.sourceType, "live_recording");
  assert.equal(asset?.status, "ready");

  const liveStream = await liveStreams.byId("live_1");
  assert.equal(liveStream?.recordingAssetId, out.assetId);
});
