import test from "node:test";
import assert from "node:assert/strict";

import { ConfigSchema } from "../../src/config.js";

test("ConfigSchema parses a minimal valid env shape", () => {
  const cfg = ConfigSchema.parse({
    listenPort: 3001,
    logLevel: "info",
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
    routeFailureThreshold: 2,
    routeCooldownMs: 30000,
  });
  assert.equal(cfg.listenPort, 3001);
  assert.equal(cfg.brokerUrl, "http://broker:8080");
});

test("ConfigSchema rejects pepper shorter than 16 chars", () => {
  assert.throws(() =>
    ConfigSchema.parse({
      listenPort: 3001,
      logLevel: "info",
      brokerUrl: "http://broker:8080",
      payerDaemonSocket: "/var/run/livepeer/payer-daemon.sock",
      payerDefaultFaceValueWei: "1000",
      vtuberDefaultOffering: "default",
      vtuberSessionDefaultTtlSeconds: 3600,
      vtuberSessionBearerTtlSeconds: 7200,
      vtuberRateCardUsdPerSecond: "0.01",
      vtuberWorkerCallTimeoutMs: 15000,
      vtuberRelayMaxPerSession: 8,
      vtuberSessionBearerPepper: "short",
      vtuberWorkerControlBearerPepper: "another-test-pepper-min-16-chars",
      routeFailureThreshold: 2,
      routeCooldownMs: 30000,
    }),
  );
});
