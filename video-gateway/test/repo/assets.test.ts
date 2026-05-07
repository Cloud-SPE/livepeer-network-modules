import { test } from "node:test";
import assert from "node:assert/strict";

import { createInMemoryAssetRepo } from "../../src/testing/repoFakes.js";

test("assetRepo: insert + byId roundtrip", async () => {
  const repo = createInMemoryAssetRepo();
  const inserted = await repo.insert({
    id: "asset_1",
    projectId: "proj_1",
    status: "preparing",
    sourceType: "upload",
    encodingTier: "baseline",
  });
  assert.equal(inserted.id, "asset_1");
  const fetched = await repo.byId("asset_1");
  assert.ok(fetched);
  assert.equal(fetched.projectId, "proj_1");
});

test("assetRepo: softDelete sets deletedAt + status=deleted", async () => {
  const repo = createInMemoryAssetRepo();
  await repo.insert({
    id: "asset_2",
    projectId: "p1",
    status: "ready",
    sourceType: "upload",
    encodingTier: "baseline",
  });
  const at = new Date("2026-05-07T00:00:00Z");
  await repo.softDelete("asset_2", at);
  const after = await repo.byId("asset_2");
  assert.ok(after);
  assert.equal(after.status, "deleted");
  assert.equal(after.deletedAt?.getTime(), at.getTime());
});

test("assetRepo: list excludes soft-deleted by default", async () => {
  const repo = createInMemoryAssetRepo();
  await repo.insert({
    id: "a1",
    projectId: "p1",
    status: "ready",
    sourceType: "upload",
    encodingTier: "baseline",
  });
  await repo.insert({
    id: "a2",
    projectId: "p1",
    status: "deleted",
    sourceType: "upload",
    encodingTier: "baseline",
    deletedAt: new Date(),
  });
  const list = await repo.list({ projectId: "p1", limit: 10 });
  assert.equal(list.items.length, 1);
  assert.equal(list.items[0]!.id, "a1");
});

test("assetRepo: list includeDeleted returns all rows", async () => {
  const repo = createInMemoryAssetRepo();
  await repo.insert({
    id: "a1",
    projectId: "p1",
    status: "ready",
    sourceType: "upload",
    encodingTier: "baseline",
  });
  await repo.insert({
    id: "a2",
    projectId: "p1",
    status: "deleted",
    sourceType: "upload",
    encodingTier: "baseline",
    deletedAt: new Date(),
  });
  const list = await repo.list({ projectId: "p1", limit: 10, includeDeleted: true });
  assert.equal(list.items.length, 2);
});

test("assetRepo: updateStatus sets status + readyAt", async () => {
  const repo = createInMemoryAssetRepo();
  await repo.insert({
    id: "a1",
    projectId: "p1",
    status: "preparing",
    sourceType: "upload",
    encodingTier: "baseline",
  });
  const readyAt = new Date("2026-05-07T01:00:00Z");
  await repo.updateStatus("a1", "ready", { readyAt });
  const after = await repo.byId("a1");
  assert.equal(after?.status, "ready");
  assert.equal(after?.readyAt?.getTime(), readyAt.getTime());
});
