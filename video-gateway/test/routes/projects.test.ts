import assert from "node:assert/strict";
import { test } from "node:test";

import Fastify from "fastify";

import { registerProjects } from "../../src/routes/projects.js";

test("project routes: create, list, update, get, and delete flow through the project service", async () => {
  const rows = new Map([
    [
      "proj_1",
      {
        id: "proj_1",
        customerId: "cust_1",
        name: "Default project",
        createdAt: new Date("2026-05-11T12:00:00Z"),
      },
    ],
  ]);

  const app = Fastify();
  registerProjects(app, {
    service: {
      async list(customerId) {
        return [...rows.values()].filter((row) => row.customerId === customerId);
      },
      async get(projectId) {
        return rows.get(projectId) ?? null;
      },
      async create(input) {
        const row = {
          id: "proj_2",
          customerId: input.customerId,
          name: input.name,
          createdAt: new Date("2026-05-11T13:00:00Z"),
        };
        rows.set(row.id, row);
        return row;
      },
      async rename(input) {
        const row = rows.get(input.projectId);
        if (!row) return null;
        const renamed = { ...row, name: input.name };
        rows.set(input.projectId, renamed);
        return renamed;
      },
      async summarize(projectId) {
        if (projectId === "proj_2") {
          return { assets: 0, uploads: 0, liveStreams: 0, webhooks: 0 };
        }
        return { assets: 1, uploads: 0, liveStreams: 0, webhooks: 0 };
      },
      async remove(projectId) {
        return rows.delete(projectId);
      },
    },
  });

  const created = await app.inject({
    method: "POST",
    url: "/v1/projects",
    payload: {
      customer_id: "cust_1",
      name: "Uploads",
    },
  });
  assert.equal(created.statusCode, 201);
  assert.equal(created.json().name, "Uploads");

  const listed = await app.inject({
    method: "GET",
    url: "/v1/projects?customer_id=cust_1",
  });
  assert.equal(listed.statusCode, 200);
  assert.equal(listed.json().items.length, 2);

  const fetched = await app.inject({
    method: "GET",
    url: "/v1/projects/proj_2?customer_id=cust_1",
  });
  assert.equal(fetched.statusCode, 200);
  assert.deepEqual(fetched.json().usage, {
    assets: 0,
    uploads: 0,
    live_streams: 0,
    webhooks: 0,
  });

  const renamed = await app.inject({
    method: "PATCH",
    url: "/v1/projects/proj_2",
    payload: {
      customer_id: "cust_1",
      name: "Archive",
    },
  });
  assert.equal(renamed.statusCode, 200);
  assert.equal(renamed.json().name, "Archive");

  const blockedDelete = await app.inject({
    method: "DELETE",
    url: "/v1/projects/proj_1?customer_id=cust_1",
  });
  assert.equal(blockedDelete.statusCode, 409);
  assert.deepEqual(blockedDelete.json(), {
    error: "project_not_empty",
    usage: {
      assets: 1,
      uploads: 0,
      live_streams: 0,
      webhooks: 0,
    },
  });

  const deleted = await app.inject({
    method: "DELETE",
    url: "/v1/projects/proj_2?customer_id=cust_1",
  });
  assert.equal(deleted.statusCode, 204);
  assert.equal(rows.has("proj_2"), false);
});

test("project routes: ownership mismatch returns 404", async () => {
  const app = Fastify();
  registerProjects(app, {
    service: {
      async list() {
        return [];
      },
      async get() {
        return {
          id: "proj_1",
          customerId: "cust_real",
          name: "Real",
          createdAt: new Date("2026-05-11T12:00:00Z"),
        };
      },
      async create() {
        throw new Error("unreachable");
      },
      async rename() {
        return null;
      },
      async summarize() {
        return { assets: 0, uploads: 0, liveStreams: 0, webhooks: 0 };
      },
      async remove() {
        return false;
      },
    },
  });

  const res = await app.inject({
    method: "GET",
    url: "/v1/projects/proj_1?customer_id=cust_other",
  });
  assert.equal(res.statusCode, 404);
  assert.deepEqual(res.json(), { error: "project_not_found" });
});
