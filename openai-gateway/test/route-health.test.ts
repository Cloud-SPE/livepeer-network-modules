import test from "node:test";
import assert from "node:assert/strict";

import { RouteHealthTracker } from "../src/service/routeHealth.js";
import type { RouteCandidate } from "../src/service/routeSelector.js";

const candidateA: RouteCandidate = {
  brokerUrl: "https://broker-a.example.com",
  capability: "openai:chat-completions",
  offering: "model-a",
  model: "model-a",
  interactionMode: "http-reqresp@v0",
  ethAddress: "0xaaa",
  pricePerWorkUnitWei: "100",
  workUnit: "tokens",
  extra: null,
  constraints: null,
};

const candidateB: RouteCandidate = {
  ...candidateA,
  brokerUrl: "https://broker-b.example.com",
  ethAddress: "0xbbb",
};

test("route health tracker ranks cooling routes behind ready routes", () => {
  const tracker = new RouteHealthTracker({ failureThreshold: 2, cooldownMs: 30_000 });

  tracker.record(candidateA, { ok: false, retryable: true }, "timeout", 1_000);
  tracker.record(candidateA, { ok: false, retryable: true }, "timeout", 2_000);

  const ranked = tracker.rankCandidates([candidateA, candidateB], 3_000);
  assert.equal(ranked[0]?.brokerUrl, candidateB.brokerUrl);
  assert.equal(ranked[1]?.brokerUrl, candidateA.brokerUrl);
});

test("route health tracker falls back to cooling routes when no ready routes remain", () => {
  const tracker = new RouteHealthTracker({ failureThreshold: 1, cooldownMs: 30_000 });

  tracker.record(candidateA, { ok: false, retryable: true }, "timeout", 1_000);

  const ranked = tracker.rankCandidates([candidateA], 2_000);
  assert.equal(ranked.length, 1);
  assert.equal(ranked[0]?.brokerUrl, candidateA.brokerUrl);
});

test("route health tracker resets failure state after success", () => {
  const tracker = new RouteHealthTracker({ failureThreshold: 1, cooldownMs: 30_000 });

  tracker.record(candidateA, { ok: false, retryable: true }, "timeout", 1_000);
  tracker.record(candidateA, { ok: true, retryable: false }, undefined, 2_000);

  const ranked = tracker.rankCandidates([candidateA, candidateB], 3_000);
  assert.equal(ranked[0]?.brokerUrl, candidateA.brokerUrl);

  const snapshot = tracker.inspect(3_000);
  assert.equal(snapshot[0]?.consecutiveFailures, 0);
  assert.equal(snapshot[0]?.coolingDown, false);
  assert.equal(snapshot[0]?.lastSuccessAt, 2_000);
});
