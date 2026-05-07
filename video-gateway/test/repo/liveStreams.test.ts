import { test } from "node:test";
import assert from "node:assert/strict";

import { createInMemoryLiveStreamRepo } from "../../src/testing/repoFakes.js";

test("liveStreamRepo: insert + byStreamKeyHash lookup", async () => {
  const repo = createInMemoryLiveStreamRepo();
  await repo.insert({
    id: "ls_1",
    projectId: "p1",
    streamKeyHash: "hash_abc",
    status: "idle",
    ingestProtocol: "rtmp",
    recordingEnabled: false,
  });
  const found = await repo.byStreamKeyHash("hash_abc");
  assert.ok(found);
  assert.equal(found.id, "ls_1");
});

test("liveStreamRepo: updateStatus moves session through lifecycle", async () => {
  const repo = createInMemoryLiveStreamRepo();
  await repo.insert({
    id: "ls_1",
    projectId: "p1",
    streamKeyHash: "h1",
    status: "idle",
    ingestProtocol: "rtmp",
    recordingEnabled: false,
  });
  await repo.updateStatus("ls_1", "active", { sessionId: "sess_1" });
  const after = await repo.byId("ls_1");
  assert.equal(after?.status, "active");
  assert.equal(after?.sessionId, "sess_1");
});

test("liveStreamRepo: active() returns active + reconnecting only", async () => {
  const repo = createInMemoryLiveStreamRepo();
  await repo.insert({
    id: "a",
    projectId: "p1",
    streamKeyHash: "h_a",
    status: "active",
    ingestProtocol: "rtmp",
    recordingEnabled: false,
  });
  await repo.insert({
    id: "b",
    projectId: "p1",
    streamKeyHash: "h_b",
    status: "ended",
    ingestProtocol: "rtmp",
    recordingEnabled: false,
  });
  await repo.insert({
    id: "c",
    projectId: "p1",
    streamKeyHash: "h_c",
    status: "reconnecting",
    ingestProtocol: "rtmp",
    recordingEnabled: false,
  });
  const active = await repo.active();
  assert.equal(active.length, 2);
});

test("liveStreamRepo: sweepStale returns active streams past cutoff", async () => {
  const repo = createInMemoryLiveStreamRepo();
  const old = new Date("2026-05-01T00:00:00Z");
  const fresh = new Date("2026-05-07T00:00:00Z");
  await repo.insert({
    id: "stale",
    projectId: "p1",
    streamKeyHash: "h1",
    status: "active",
    ingestProtocol: "rtmp",
    recordingEnabled: false,
    lastSeenAt: old,
  });
  await repo.insert({
    id: "fresh",
    projectId: "p1",
    streamKeyHash: "h2",
    status: "active",
    ingestProtocol: "rtmp",
    recordingEnabled: false,
    lastSeenAt: fresh,
  });
  const cutoff = new Date("2026-05-06T00:00:00Z");
  const stale = await repo.sweepStale(cutoff);
  assert.equal(stale.length, 1);
  assert.equal(stale[0]!.id, "stale");
});
