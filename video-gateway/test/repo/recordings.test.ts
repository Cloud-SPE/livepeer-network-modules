import { test } from "node:test";
import assert from "node:assert/strict";

import { createInMemoryRecordingRepo } from "../../src/testing/repoFakes.js";

test("recordingRepo: insert + byId roundtrip", async () => {
  const repo = createInMemoryRecordingRepo();
  await repo.insert({
    id: "rec_1",
    liveStreamId: "ls_1",
    assetId: null,
    status: "pending",
    startedAt: null,
    endedAt: null,
  });
  const r = await repo.byId("rec_1");
  assert.equal(r?.status, "pending");
});

test("recordingRepo: updateStatus moves through ready + sets assetId", async () => {
  const repo = createInMemoryRecordingRepo();
  await repo.insert({
    id: "rec_1",
    liveStreamId: "ls_1",
    assetId: null,
    status: "pending",
    startedAt: null,
    endedAt: null,
  });
  await repo.updateStatus("rec_1", "ready", { assetId: "asset_X", endedAt: new Date() });
  const r = await repo.byId("rec_1");
  assert.equal(r?.status, "ready");
  assert.equal(r?.assetId, "asset_X");
});

test("recordingRepo: byLiveStream returns rows for that stream only", async () => {
  const repo = createInMemoryRecordingRepo();
  await repo.insert({
    id: "rec_1",
    liveStreamId: "ls_1",
    assetId: null,
    status: "pending",
    startedAt: null,
    endedAt: null,
  });
  await repo.insert({
    id: "rec_2",
    liveStreamId: "ls_2",
    assetId: null,
    status: "pending",
    startedAt: null,
    endedAt: null,
  });
  const list = await repo.byLiveStream("ls_1");
  assert.equal(list.length, 1);
  assert.equal(list[0]!.id, "rec_1");
});
