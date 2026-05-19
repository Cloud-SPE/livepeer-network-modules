import test from "node:test";
import assert from "node:assert/strict";

import { isQuoteRejection } from "../src/service/routeDispatch.js";
import { LivepeerBrokerError, gatewayHttpStatusFor } from "../src/livepeer/errors.js";

test("isQuoteRejection matches payer-daemon quote_ref validation errors", () => {
  assert.equal(isQuoteRejection(new Error("quote_ref.constraint_fingerprint is empty")), true);
  assert.equal(isQuoteRejection(new Error("quote_ref.route_fingerprint is empty")), true);
  assert.equal(isQuoteRejection(new Error("quote_ref.quote_id is empty")), true);
});

test("isQuoteRejection matches resolver missing-fingerprint failures", () => {
  assert.equal(
    isQuoteRejection(new Error("9 FAILED_PRECONDITION: resolver selected route missing quote fingerprints")),
    true,
  );
});

test("isQuoteRejection ignores unrelated dispatch failures", () => {
  assert.equal(isQuoteRejection(new Error("upstream timed out")), false);
  assert.equal(isQuoteRejection(new Error("no route candidates for capability=x offering=y")), false);
  assert.equal(isQuoteRejection(null), false);
  assert.equal(isQuoteRejection(undefined), false);
});

test("gatewayHttpStatusFor preserves 503 for broker_quote_rejected", () => {
  const rejected = new LivepeerBrokerError({
    status: 503,
    code: "broker_quote_rejected",
    message: "quote_ref.constraint_fingerprint is empty",
  });
  assert.equal(gatewayHttpStatusFor(rejected), 503);
});

test("gatewayHttpStatusFor squashes upstream 5xx to 502 by default", () => {
  const upstream500 = new LivepeerBrokerError({ status: 500, code: "upstream_unknown", message: "boom" });
  const upstream502 = new LivepeerBrokerError({ status: 502, code: "bad_gateway", message: "boom" });
  const upstream504 = new LivepeerBrokerError({ status: 504, code: "gateway_timeout", message: "boom" });
  assert.equal(gatewayHttpStatusFor(upstream500), 502);
  assert.equal(gatewayHttpStatusFor(upstream502), 502);
  assert.equal(gatewayHttpStatusFor(upstream504), 502);
});

test("gatewayHttpStatusFor passes 4xx through unchanged", () => {
  const upstream429 = new LivepeerBrokerError({ status: 429, code: "rate_limited", message: "slow down" });
  const upstream400 = new LivepeerBrokerError({ status: 400, code: "bad_request", message: "nope" });
  assert.equal(gatewayHttpStatusFor(upstream429), 429);
  assert.equal(gatewayHttpStatusFor(upstream400), 400);
});
