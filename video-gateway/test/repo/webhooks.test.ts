import { test } from "node:test";
import assert from "node:assert/strict";

import {
  createInMemoryWebhookDeliveryRepo,
  createInMemoryWebhookEndpointRepo,
} from "../../src/testing/repoFakes.js";

test("webhookEndpointRepo: byProject returns active endpoints only", async () => {
  const repo = createInMemoryWebhookEndpointRepo();
  await repo.insert({
    id: "ep_1",
    projectId: "p1",
    url: "https://hooks.example.com/a",
    secret: "secret_a",
    eventTypes: null,
    createdAt: new Date(),
  });
  await repo.insert({
    id: "ep_2",
    projectId: "p1",
    url: "https://hooks.example.com/b",
    secret: "secret_b",
    eventTypes: null,
    createdAt: new Date(),
    disabledAt: new Date(),
  });
  const list = await repo.byProject("p1");
  assert.equal(list.length, 1);
  assert.equal(list[0]!.id, "ep_1");
});

test("webhookEndpointRepo: disable flips disabledAt", async () => {
  const repo = createInMemoryWebhookEndpointRepo();
  await repo.insert({
    id: "ep_1",
    projectId: "p1",
    url: "https://x",
    secret: "s",
    eventTypes: null,
    createdAt: new Date(),
  });
  const at = new Date("2026-05-07T00:00:00Z");
  await repo.disable("ep_1", at);
  const ep = await repo.byId("ep_1");
  assert.equal(ep?.disabledAt?.getTime(), at.getTime());
});

test("webhookDeliveryRepo: markDelivered sets status + deliveredAt", async () => {
  const repo = createInMemoryWebhookDeliveryRepo();
  await repo.insert({
    id: "del_1",
    endpointId: "ep_1",
    eventType: "video.asset.ready",
    status: "pending",
    attempts: 0,
    createdAt: new Date(),
    body: '{"hello":"world"}',
    signatureHeader: "sha256=abc",
  });
  const at = new Date("2026-05-07T00:00:00Z");
  await repo.markDelivered("del_1", 1, at);
  const after = await repo.byId("del_1");
  assert.equal(after?.status, "delivered");
  assert.equal(after?.attempts, 1);
});

test("webhookDeliveryRepo: byEndpoint orders newest-first + applies limit", async () => {
  const repo = createInMemoryWebhookDeliveryRepo();
  for (let i = 0; i < 5; i++) {
    await repo.insert({
      id: `del_${i}`,
      endpointId: "ep_1",
      eventType: "video.asset.ready",
      status: "pending",
      attempts: 0,
      createdAt: new Date(2026, 4, 7, 0, 0, i),
      body: "{}",
      signatureHeader: "sha256=x",
    });
  }
  const list = await repo.byEndpoint("ep_1", { limit: 3 });
  assert.equal(list.length, 3);
  assert.equal(list[0]!.id, "del_4");
});
