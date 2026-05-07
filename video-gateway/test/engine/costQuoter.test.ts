import { test } from "node:test";
import assert from "node:assert/strict";

import { estimateCost, reportUsage } from "../../src/engine/service/costQuoter.js";
import { defaultPricingConfig } from "../../src/engine/config/pricing.js";

test("estimateCost: VOD over 60s with two h264 renditions", () => {
  const pricing = defaultPricingConfig();
  const quote = estimateCost({
    capability: "video:transcode.vod",
    callerTier: "free",
    renditions: [
      { resolution: "720p", codec: "h264", bitrateKbps: 2800 },
      { resolution: "1080p", codec: "h264", bitrateKbps: 5000 },
    ],
    estimatedSeconds: 60,
    pricing,
  });
  assert.equal(quote.callerTier, "free");
  assert.equal(quote.estimatedSeconds, 60);
  assert.equal(quote.renditions.length, 2);
  assert.ok(quote.cents >= 1);
});

test("estimateCost: live stream returns overhead-only quote", () => {
  const pricing = defaultPricingConfig();
  const quote = estimateCost({
    capability: "video:live.rtmp",
    callerTier: "prepaid",
    renditions: [{ resolution: "720p", codec: "h264", bitrateKbps: 2800 }],
    estimatedSeconds: null,
    pricing,
  });
  assert.equal(quote.estimatedSeconds, null);
  assert.equal(quote.cents, pricing.overheadCents);
});

test("reportUsage: total = sum-per-second * actualSeconds", () => {
  const pricing = defaultPricingConfig();
  const usage = reportUsage({
    capability: "video:transcode.vod",
    renditions: [{ resolution: "1080p", codec: "h264", bitrateKbps: 5000 }],
    actualSeconds: 100,
    pricing,
  });
  assert.equal(usage.actualSeconds, 100);
  assert.equal(usage.cents, Math.ceil(0.008 * 100));
});
