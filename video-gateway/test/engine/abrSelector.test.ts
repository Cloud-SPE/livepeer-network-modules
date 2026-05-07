import { test } from "node:test";
import assert from "node:assert/strict";

import { selectAbrLadder } from "../../src/service/abrSelector.js";

test("free tier → baseline ladder (h264 only)", () => {
  const ladder = selectAbrLadder({ customerTier: "free", policy: "customer-tier" });
  assert.ok(ladder.length >= 5);
  for (const r of ladder) assert.equal(r.codec, "h264");
});

test("prepaid tier → standard ladder (h264 + hevc)", () => {
  const ladder = selectAbrLadder({ customerTier: "prepaid", policy: "customer-tier" });
  const codecs = new Set(ladder.map((r) => r.codec));
  assert.deepEqual([...codecs].sort(), ["h264", "hevc"]);
});

test("enterprise tier → premium ladder (h264 + hevc + av1)", () => {
  const ladder = selectAbrLadder({
    customerTier: "enterprise",
    policy: "customer-tier",
  });
  const codecs = new Set(ladder.map((r) => r.codec));
  assert.deepEqual([...codecs].sort(), ["av1", "h264", "hevc"]);
});
