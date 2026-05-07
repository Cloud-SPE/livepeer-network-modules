import { test } from "node:test";
import assert from "node:assert/strict";

import { parsePublishingPath } from "../../src/runtime/rtmp/keyParser.js";

test("parsePublishingPath: bare stream key", () => {
  const out = parsePublishingPath("/sk_abc123");
  assert.equal(out.streamKey, "sk_abc123");
  assert.equal(out.apiKey, undefined);
});

test("parsePublishingPath: api-key/stream-key two-segment path", () => {
  const out = parsePublishingPath("/lp_apikey_123/sk_abc456");
  assert.equal(out.apiKey, "lp_apikey_123");
  assert.equal(out.streamKey, "sk_abc456");
});

test("parsePublishingPath: empty path", () => {
  const out = parsePublishingPath("/");
  assert.equal(out.streamKey, "");
});
