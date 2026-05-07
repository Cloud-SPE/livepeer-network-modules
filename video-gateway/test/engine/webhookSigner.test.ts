import { test } from "node:test";
import assert from "node:assert/strict";

import {
  signEvent,
  verifyEvent,
} from "../../src/engine/service/webhookSigner.js";

test("signEvent emits X-Livepeer-Signature with sha256= prefix", () => {
  const headers = signEvent({
    secret: "secret-pepper",
    body: '{"hello":"world"}',
    eventType: "video.asset.ready",
    deliveryId: "wh_delivery_test",
    timestamp: 1700000000,
  });
  assert.match(headers["X-Livepeer-Signature"], /^sha256=[0-9a-f]{64}$/);
  assert.equal(headers["X-Livepeer-Event-Type"], "video.asset.ready");
  assert.equal(headers["X-Livepeer-Delivery-Id"], "wh_delivery_test");
  assert.equal(headers["X-Livepeer-Timestamp"], "1700000000");
});

test("verifyEvent passes for valid signature within tolerance", () => {
  const body = '{"hello":"world"}';
  const ts = 1700000000;
  const signed = signEvent({
    secret: "secret-pepper",
    body,
    eventType: "video.asset.ready",
    deliveryId: "wh_delivery_test",
    timestamp: ts,
  });
  const ok = verifyEvent({
    secret: "secret-pepper",
    body,
    signature: signed["X-Livepeer-Signature"],
    timestamp: signed["X-Livepeer-Timestamp"],
    now: ts + 30,
  });
  assert.equal(ok, true);
});

test("verifyEvent rejects mismatched body", () => {
  const ts = 1700000000;
  const signed = signEvent({
    secret: "secret-pepper",
    body: "original",
    eventType: "video.asset.ready",
    deliveryId: "x",
    timestamp: ts,
  });
  const ok = verifyEvent({
    secret: "secret-pepper",
    body: "tampered",
    signature: signed["X-Livepeer-Signature"],
    timestamp: signed["X-Livepeer-Timestamp"],
    now: ts + 1,
  });
  assert.equal(ok, false);
});
