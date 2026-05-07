import { test } from "node:test";
import assert from "node:assert/strict";

import { createRetryDispatcher } from "../../src/service/webhookDispatcher.js";
import {
  createInMemoryWebhookDeliveryRepo,
  createInMemoryWebhookEndpointRepo,
  createInMemoryWebhookFailureRepo,
} from "../../src/testing/repoFakes.js";
import type { WebhookEvent } from "../../src/engine/types/index.js";

function fakeEvent(): WebhookEvent {
  return {
    type: "video.asset.ready",
    occurredAt: new Date("2026-05-07T00:00:00Z"),
    data: { id: "asset_1" },
  };
}

function makeRepos() {
  const endpoints = createInMemoryWebhookEndpointRepo();
  const deliveries = createInMemoryWebhookDeliveryRepo();
  const failures = createInMemoryWebhookFailureRepo();
  return { endpoints, deliveries, failures };
}

async function seedDelivery(repos: ReturnType<typeof makeRepos>): Promise<string> {
  await repos.endpoints.insert({
    id: "ep_1",
    projectId: "p1",
    url: "https://hooks.example.com/wh",
    secret: "secret_seed",
    eventTypes: null,
    createdAt: new Date(),
  });
  await repos.deliveries.insert({
    id: "del_1",
    endpointId: "ep_1",
    eventType: "video.asset.ready",
    status: "pending",
    attempts: 0,
    createdAt: new Date(),
    body: "",
    signatureHeader: "",
  });
  return "del_1";
}

test("dispatch: 200 first attempt → delivered, 1 attempt", async () => {
  const repos = makeRepos();
  await seedDelivery(repos);
  const calls: string[] = [];
  const fetchImpl = (async (url: string) => {
    calls.push(String(url));
    return new Response(null, { status: 200 });
  }) as typeof fetch;

  const dispatcher = createRetryDispatcher(
    { pepper: "p", fetchImpl, sleep: async () => {} },
    repos,
  );
  const out = await dispatcher.dispatch({
    endpointId: "ep_1",
    endpointUrl: "https://hooks.example.com/wh",
    endpointSecret: "secret_seed",
    deliveryId: "del_1",
    event: fakeEvent(),
  });
  assert.equal(out.delivered, true);
  assert.equal(out.attempts, 1);
  assert.equal(calls.length, 1);
  const after = await repos.deliveries.byId("del_1");
  assert.equal(after?.status, "delivered");
  assert.equal(repos.failures.rows.size, 0);
});

test("dispatch: 503 then 200 → delivered, 2 attempts, no dead-letter", async () => {
  const repos = makeRepos();
  await seedDelivery(repos);
  let n = 0;
  const fetchImpl = (async () => {
    n++;
    if (n === 1) return new Response(null, { status: 503 });
    return new Response(null, { status: 200 });
  }) as typeof fetch;

  const sleeps: number[] = [];
  const dispatcher = createRetryDispatcher(
    {
      pepper: "p",
      fetchImpl,
      sleep: async (ms) => {
        sleeps.push(ms);
      },
    },
    repos,
  );
  const out = await dispatcher.dispatch({
    endpointId: "ep_1",
    endpointUrl: "https://hooks.example.com/wh",
    endpointSecret: "secret_seed",
    deliveryId: "del_1",
    event: fakeEvent(),
  });
  assert.equal(out.delivered, true);
  assert.equal(out.attempts, 2);
  assert.deepEqual(sleeps, [1000]);
  assert.equal(repos.failures.rows.size, 0);
});

test("dispatch: 503 four times → dead-lettered, 4 attempts, backoffs 1s/5s/30s", async () => {
  const repos = makeRepos();
  await seedDelivery(repos);
  const fetchImpl = (async () => new Response(null, { status: 503 })) as typeof fetch;
  const sleeps: number[] = [];
  const dispatcher = createRetryDispatcher(
    {
      pepper: "p",
      fetchImpl,
      sleep: async (ms) => {
        sleeps.push(ms);
      },
      newId: () => "whf_X",
    },
    repos,
  );
  const out = await dispatcher.dispatch({
    endpointId: "ep_1",
    endpointUrl: "https://hooks.example.com/wh",
    endpointSecret: "secret_seed",
    deliveryId: "del_1",
    event: fakeEvent(),
  });
  assert.equal(out.delivered, false);
  assert.equal(out.attempts, 4);
  assert.deepEqual(sleeps, [1000, 5000, 30000]);
  assert.equal(out.failureId, "whf_X");
  const after = await repos.deliveries.byId("del_1");
  assert.equal(after?.status, "failed");
  assert.equal(after?.attempts, 4);
  assert.equal(repos.failures.rows.size, 1);
  const f = repos.failures.rows.get("whf_X");
  assert.equal(f?.statusCode, 503);
});

test("dispatch: 4xx → immediate dead-letter, no retry, no sleeps", async () => {
  const repos = makeRepos();
  await seedDelivery(repos);
  let calls = 0;
  const fetchImpl = (async () => {
    calls++;
    return new Response(null, { status: 404 });
  }) as typeof fetch;
  const sleeps: number[] = [];
  const dispatcher = createRetryDispatcher(
    {
      pepper: "p",
      fetchImpl,
      sleep: async (ms) => {
        sleeps.push(ms);
      },
      newId: () => "whf_4xx",
    },
    repos,
  );
  const out = await dispatcher.dispatch({
    endpointId: "ep_1",
    endpointUrl: "https://hooks.example.com/wh",
    endpointSecret: "secret_seed",
    deliveryId: "del_1",
    event: fakeEvent(),
  });
  assert.equal(out.delivered, false);
  assert.equal(out.attempts, 1);
  assert.equal(calls, 1);
  assert.deepEqual(sleeps, []);
  assert.equal(repos.failures.rows.size, 1);
  const f = repos.failures.rows.get("whf_4xx");
  assert.equal(f?.statusCode, 404);
});

test("dispatch: network error retries → exhausts → dead-letters", async () => {
  const repos = makeRepos();
  await seedDelivery(repos);
  const fetchImpl = (async () => {
    throw new Error("ECONNREFUSED");
  }) as typeof fetch;
  const dispatcher = createRetryDispatcher(
    {
      pepper: "p",
      fetchImpl,
      sleep: async () => {},
      newId: () => "whf_net",
    },
    repos,
  );
  const out = await dispatcher.dispatch({
    endpointId: "ep_1",
    endpointUrl: "https://hooks.example.com/wh",
    endpointSecret: "secret_seed",
    deliveryId: "del_1",
    event: fakeEvent(),
  });
  assert.equal(out.delivered, false);
  assert.equal(out.attempts, 4);
  assert.equal(out.lastError, "ECONNREFUSED");
  assert.equal(repos.failures.rows.size, 1);
  assert.equal(repos.failures.rows.get("whf_net")?.statusCode, null);
});

test("replayFailure: dead-lettered → second time succeeds → markReplayed", async () => {
  const repos = makeRepos();
  await seedDelivery(repos);
  let n = 0;
  const fetchImpl = (async () => {
    n++;
    if (n <= 4) return new Response(null, { status: 503 });
    return new Response(null, { status: 200 });
  }) as typeof fetch;
  const dispatcher = createRetryDispatcher(
    {
      pepper: "p",
      fetchImpl,
      sleep: async () => {},
      newId: () => "whf_R",
    },
    repos,
  );
  const first = await dispatcher.dispatch({
    endpointId: "ep_1",
    endpointUrl: "https://hooks.example.com/wh",
    endpointSecret: "secret_seed",
    deliveryId: "del_1",
    event: fakeEvent(),
  });
  assert.equal(first.delivered, false);
  assert.equal(first.failureId, "whf_R");

  const replayed = await dispatcher.replayFailure("whf_R");
  assert.equal(replayed.delivered, true);
  const f = repos.failures.rows.get("whf_R");
  assert.ok(f?.replayedAt instanceof Date);
});
