import test from "node:test";
import assert from "node:assert/strict";

import { ConfigSchema } from "../../src/config.js";
import { buildServer } from "../../src/server.js";

function testConfig() {
  return ConfigSchema.parse({
    listenPort: 3001,
    logLevel: "fatal",
    brokerUrl: "http://broker:8080",
    payerDaemonSocket: "/var/run/livepeer/payer-daemon.sock",
    payerDefaultFaceValueWei: "1000000000000000",
    vtuberDefaultOffering: "default",
    vtuberSessionDefaultTtlSeconds: 3600,
    vtuberSessionBearerTtlSeconds: 7200,
    vtuberRateCardUsdPerSecond: "0.01",
    vtuberWorkerCallTimeoutMs: 15000,
    vtuberRelayMaxPerSession: 8,
    vtuberSessionBearerPepper: "this-is-a-test-pepper-min-16-chars",
    vtuberWorkerControlBearerPepper: "another-test-pepper-min-16-chars",
  });
}

test("GET /healthz returns ok", async () => {
  const app = await buildServer(testConfig());
  try {
    const resp = await app.inject({ method: "GET", url: "/healthz" });
    assert.equal(resp.statusCode, 200);
  } finally {
    await app.close();
  }
});

test("POST /v1/vtuber/sessions rejects invalid bodies with 400", async () => {
  const app = await buildServer(testConfig());
  try {
    const resp = await app.inject({
      method: "POST",
      url: "/v1/vtuber/sessions",
      headers: { "content-type": "application/json" },
      payload: { persona: "x" },
    });
    assert.equal(resp.statusCode, 400);
  } finally {
    await app.close();
  }
});

test("POST /v1/vtuber/sessions accepts well-formed bodies (returns 503 unimplemented)", async () => {
  const app = await buildServer(testConfig());
  try {
    const resp = await app.inject({
      method: "POST",
      url: "/v1/vtuber/sessions",
      headers: { "content-type": "application/json" },
      payload: {
        persona: "grifter",
        vrm_url: "https://example.com/avatar.vrm",
        llm_provider: "livepeer",
        tts_provider: "livepeer",
      },
    });
    assert.equal(resp.statusCode, 503);
    assert.match(resp.payload, /unimplemented/);
  } finally {
    await app.close();
  }
});

test("GET /v1/vtuber/sessions/:id rejects malformed UUIDs", async () => {
  const app = await buildServer(testConfig());
  try {
    const resp = await app.inject({
      method: "GET",
      url: "/v1/vtuber/sessions/not-a-uuid",
    });
    assert.equal(resp.statusCode, 400);
  } finally {
    await app.close();
  }
});

test("POST /v1/vtuber/sessions/:id/topup rejects non-positive cents", async () => {
  const app = await buildServer(testConfig());
  try {
    const resp = await app.inject({
      method: "POST",
      url: "/v1/vtuber/sessions/00000000-0000-0000-0000-000000000001/topup",
      headers: { "content-type": "application/json" },
      payload: { cents: -1 },
    });
    assert.equal(resp.statusCode, 400);
  } finally {
    await app.close();
  }
});

test("POST /v1/stripe/webhook requires a stripe-signature header", async () => {
  const app = await buildServer(testConfig());
  try {
    const resp = await app.inject({
      method: "POST",
      url: "/v1/stripe/webhook",
      payload: {},
    });
    assert.equal(resp.statusCode, 400);
  } finally {
    await app.close();
  }
});
