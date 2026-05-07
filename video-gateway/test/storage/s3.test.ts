import { test } from "node:test";
import assert from "node:assert/strict";

import {
  createS3StorageProvider,
  loadS3ConfigFromEnv,
  pathFor,
} from "../../src/storage/s3.js";

const S3_TEST_CONFIG = {
  endpoint: "http://localhost:9000",
  region: "us-east-1",
  bucket: "video-test",
  accessKeyId: "rustfs",
  secretAccessKey: "rustfs1234",
  forcePathStyle: true,
};

test("pathFor: source kind generates sources/<asset>/<file> key", () => {
  const k = pathFor({ assetId: "asset_1", kind: "source", filename: "input.mp4" });
  assert.equal(k, "sources/asset_1/input.mp4");
});

test("pathFor: rendition includes codec-resolution segment", () => {
  const k = pathFor({
    assetId: "asset_1",
    kind: "rendition",
    codec: "h264",
    resolution: "1080p",
    filename: "out.m4s",
  });
  assert.equal(k, "renditions/asset_1/h264-1080p/out.m4s");
});

test("pathFor: liveSegment uses streamId", () => {
  const k = pathFor({ streamId: "live_1", kind: "liveSegment", filename: "seg_0001.m4s" });
  assert.equal(k, "live/segments/live_1/seg_0001.m4s");
});

test("loadS3ConfigFromEnv: reads vars + applies forcePathStyle default true", () => {
  const env = {
    S3_REGION: "us-west-2",
    S3_BUCKET: "b",
    S3_ACCESS_KEY_ID: "k",
    S3_SECRET_ACCESS_KEY: "s",
    S3_ENDPOINT: "http://rustfs:9000",
  };
  const cfg = loadS3ConfigFromEnv(env);
  assert.equal(cfg.region, "us-west-2");
  assert.equal(cfg.endpoint, "http://rustfs:9000");
  assert.equal(cfg.forcePathStyle, true);
});

test("loadS3ConfigFromEnv: forcePathStyle=false respects env override", () => {
  const env = {
    S3_REGION: "us-east-1",
    S3_BUCKET: "b",
    S3_ACCESS_KEY_ID: "k",
    S3_SECRET_ACCESS_KEY: "s",
    S3_FORCE_PATH_STYLE: "false",
  };
  const cfg = loadS3ConfigFromEnv(env);
  assert.equal(cfg.forcePathStyle, false);
});

test("loadS3ConfigFromEnv: missing required field throws", () => {
  const env = { S3_REGION: "x" };
  assert.throws(() => loadS3ConfigFromEnv(env), /S3_BUCKET/);
});

test("putSignedUploadUrl: produces presigned PUT url with correct storageKey", async () => {
  const provider = createS3StorageProvider(S3_TEST_CONFIG);
  const result = await provider.putSignedUploadUrl({
    assetId: "asset_1",
    kind: "source",
    filename: "in.mp4",
    contentType: "video/mp4",
    expiresInSec: 600,
  });
  assert.equal(result.storageKey, "sources/asset_1/in.mp4");
  assert.match(result.url, /^http:\/\/localhost:9000\//);
  assert.match(result.url, /X-Amz-Signature=/);
  assert.match(result.url, /X-Amz-Expires=600/);
});

test("getSignedDownloadUrl: produces presigned GET url", async () => {
  const provider = createS3StorageProvider(S3_TEST_CONFIG);
  const url = await provider.getSignedDownloadUrl({
    storageKey: "renditions/asset_1/h264-1080p/out.m4s",
    expiresInSec: 60,
  });
  assert.match(url, /renditions\/asset_1\/h264-1080p\/out\.m4s/);
  assert.match(url, /X-Amz-Signature=/);
});
