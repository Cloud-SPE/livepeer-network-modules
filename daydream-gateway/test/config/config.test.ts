import test from "node:test";
import assert from "node:assert/strict";

import { loadConfig } from "../../src/config.js";

const REQUIRED_ENV = {
  DAYDREAM_GATEWAY_PAYER_DAEMON_SOCKET: "/tmp/payer.sock",
  DAYDREAM_GATEWAY_RESOLVER_SOCKET: "/tmp/resolver.sock",
};

test("loadConfig reads LIVEPEER_ROUTE_* env vars (post-alignment with other gateways)", () => {
  const cfg = loadConfig({
    ...REQUIRED_ENV,
    LIVEPEER_ROUTE_FAILURE_THRESHOLD: "5",
    LIVEPEER_ROUTE_COOLDOWN_MS: "12345",
  });
  assert.equal(cfg.routeFailureThreshold, 5);
  assert.equal(cfg.routeCooldownMs, 12345);
});

test("loadConfig falls back to defaults when LIVEPEER_ROUTE_* are absent", () => {
  const cfg = loadConfig({ ...REQUIRED_ENV });
  assert.equal(cfg.routeFailureThreshold, 2);
  assert.equal(cfg.routeCooldownMs, 30_000);
});

test("loadConfig ignores legacy DAYDREAM_GATEWAY_ROUTE_* names (post-rename)", () => {
  const cfg = loadConfig({
    ...REQUIRED_ENV,
    DAYDREAM_GATEWAY_ROUTE_FAILURE_THRESHOLD: "99",
    DAYDREAM_GATEWAY_ROUTE_COOLDOWN_MS: "99999",
  });
  assert.equal(cfg.routeFailureThreshold, 2);
  assert.equal(cfg.routeCooldownMs, 30_000);
});
