import { test } from "node:test";
import assert from "node:assert/strict";

import { buildPayment } from "../src/livepeer/payment.js";

// Round-trip the hand-rolled encoder by decoding with a tiny, manual
// protobuf-3 reader. We re-implement the minimum we need rather than
// pulling in protobufjs — the openai-gateway is zero-runtime-deps and we
// keep dev-deps to the same bar where reasonable.

interface DecodedPayment {
  capabilityId?: string;
  offeringId?: string;
  expectedMaxUnits?: bigint;
  ticket?: Uint8Array;
}

function decodePayment(b64: string): DecodedPayment {
  const raw = Buffer.from(b64, "base64");
  const out: DecodedPayment = {};
  let i = 0;
  while (i < raw.length) {
    const [tag, after] = readVarint(raw, i);
    i = after;
    const fieldNum = Number(tag >> 3n);
    const wireType = Number(tag & 0x7n);
    switch (wireType) {
      case 0: {
        const [v, j] = readVarint(raw, i);
        i = j;
        if (fieldNum === 3) out.expectedMaxUnits = v;
        break;
      }
      case 2: {
        const [len, j] = readVarint(raw, i);
        i = j;
        const slice = raw.subarray(i, i + Number(len));
        i += Number(len);
        if (fieldNum === 1) out.capabilityId = slice.toString("utf8");
        else if (fieldNum === 2) out.offeringId = slice.toString("utf8");
        else if (fieldNum === 4) out.ticket = new Uint8Array(slice);
        break;
      }
      default:
        throw new Error(`unsupported wire type ${wireType}`);
    }
  }
  return out;
}

function readVarint(buf: Buffer, offset: number): [bigint, number] {
  let result = 0n;
  let shift = 0n;
  let i = offset;
  while (true) {
    const b = buf[i++];
    if (b === undefined) throw new Error("truncated varint");
    result |= BigInt(b & 0x7f) << shift;
    if ((b & 0x80) === 0) return [result, i];
    shift += 7n;
  }
}

test("buildPayment encodes all four fields by default", () => {
  const env = buildPayment({
    capabilityId: "openai:chat-completions:gpt-5",
    offeringId: "default",
  });
  const decoded = decodePayment(env);
  assert.equal(decoded.capabilityId, "openai:chat-completions:gpt-5");
  assert.equal(decoded.offeringId, "default");
  assert.equal(decoded.expectedMaxUnits, 10_000n);
  assert.ok(decoded.ticket);
  assert.ok(decoded.ticket!.length > 0);
});

test("buildPayment honors expectedMaxUnits override", () => {
  const env = buildPayment({
    capabilityId: "x",
    offeringId: "y",
    expectedMaxUnits: 7n,
  });
  const decoded = decodePayment(env);
  assert.equal(decoded.expectedMaxUnits, 7n);
});

test("buildPayment honors a custom ticket", () => {
  const env = buildPayment({
    capabilityId: "x",
    offeringId: "y",
    ticket: "custom-ticket-bytes",
  });
  const decoded = decodePayment(env);
  assert.equal(Buffer.from(decoded.ticket!).toString("utf8"), "custom-ticket-bytes");
});

test("buildPayment rejects empty capabilityId", () => {
  assert.throws(() => buildPayment({ capabilityId: "", offeringId: "y" }), /capabilityId/);
});

test("buildPayment rejects empty offeringId", () => {
  assert.throws(() => buildPayment({ capabilityId: "x", offeringId: "" }), /offeringId/);
});

test("buildPayment rejects zero expectedMaxUnits", () => {
  assert.throws(
    () => buildPayment({ capabilityId: "x", offeringId: "y", expectedMaxUnits: 0n }),
    /expectedMaxUnits/,
  );
});

test("buildPayment matches a known good Go-encoded value", () => {
  // Generated via `go run` of the same proto bindings:
  //   capability_id = "kibble:doggo-bark-counter:v1"
  //   offering_id   = "default"
  //   expected_max_units = 1000
  //   ticket = "smoke-stub"
  const expected =
    "ChxraWJibGU6ZG9nZ28tYmFyay1jb3VudGVyOnYxEgdkZWZhdWx0GOgHIgpzbW9rZS1zdHVi";
  const got = buildPayment({
    capabilityId: "kibble:doggo-bark-counter:v1",
    offeringId: "default",
    expectedMaxUnits: 1000n,
    ticket: "smoke-stub",
  });
  assert.equal(got, expected);
});
