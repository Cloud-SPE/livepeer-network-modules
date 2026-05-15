import test from "node:test";
import assert from "node:assert/strict";

import Fastify from "fastify";

import { registerAdmin } from "../../src/routes/admin.js";
import { createInMemorySessionStore } from "../../src/service/sessions/inMemorySessionStore.js";

const authResolver = {
  async resolve() {
    return { actor: "operator" };
  },
};

function emptyDb() {
  return {
    select() {
      return {
        from() {
          return {
            orderBy() {
              return this;
            },
            limit() {
              return Promise.resolve([]);
            },
          };
        },
      };
    },
  };
}

test("vtuber admin exposes route-health summary and Prometheus metrics", async () => {
  const app = Fastify();
  registerAdmin(app, {
    authResolver,
    db: emptyDb() as never,
    sessionStore: createInMemorySessionStore(),
    serviceRegistry: {
      async listVtuberNodes() {
        return [
          {
            nodeId: "node-1",
            nodeUrl: "http://node-1.internal:8080",
            ethAddress: "0xabc",
            capabilities: ["livepeer:vtuber-session"],
            offering: "default",
          },
        ];
      },
      async getNode() {
        return null;
      },
      async select() {
        return null;
      },
      async recordOutcome() {},
      async inspectHealth() {
        return [
          {
            key: "http://node-1.internal:8080|0xabc|livepeer:vtuber-session|default",
            consecutiveFailures: 1,
            coolingDown: true,
            cooldownUntil: 123456,
            lastFailureAt: 111111,
            lastFailureReason: "worker_start_failed",
            lastSuccessAt: 100000,
          },
        ];
      },
      async inspectMetrics() {
        return {
          attemptsTotal: 2,
          successesTotal: 1,
          retryableFailuresTotal: 1,
          nonRetryableFailuresTotal: 0,
          cooldownsOpenedTotal: 1,
        };
      },
      async close() {},
    },
    vtuberRateCardUsdPerSecond: "0.01",
  });

  const nodeHealth = await app.inject({
    method: "GET",
    url: "/admin/vtuber/node-health",
    headers: {
      authorization: "Bearer token",
      "x-actor": "operator",
    },
  });
  assert.equal(nodeHealth.statusCode, 200);
  assert.deepEqual(nodeHealth.json().summary, {
    tracked_routes: 1,
    cooling_routes: 1,
    routes_with_failures: 1,
    latest_failure_at: 111111,
    latest_success_at: 100000,
  });
  assert.deepEqual(nodeHealth.json().metrics, {
    attemptsTotal: 2,
    successesTotal: 1,
    retryableFailuresTotal: 1,
    nonRetryableFailuresTotal: 0,
    cooldownsOpenedTotal: 1,
  });

  const prom = await app.inject({
    method: "GET",
    url: "/admin/vtuber/route-health/metrics",
    headers: {
      authorization: "Bearer token",
      "x-actor": "operator",
    },
  });
  assert.equal(prom.statusCode, 200);
  assert.match(prom.headers["content-type"] ?? "", /^text\/plain/);
  assert.match(prom.body, /livepeer_gateway_route_health_attempts_total\{gateway="vtuber"\} 2/);
  assert.match(prom.body, /livepeer_gateway_route_health_cooling_routes\{gateway="vtuber"\} 1/);

  await app.close();
});
