import test from "node:test";
import assert from "node:assert/strict";

import {
  modelSupportsReqresp,
  modelSupportsStream,
  selectVariantForInteractionMode,
  selectorHeadersForVariant,
  type CatalogModelLike,
  type CatalogVariantLike,
} from "../src/frontend/portal/selection.js";

function variant(
  selectionKey: string,
  supportedModes: string[],
): CatalogVariantLike {
  return { selection_key: selectionKey, supportedModes };
}

test("model-level stream support is true when any variant advertises stream mode", () => {
  const model: CatalogModelLike = {
    supportedModes: ["http-reqresp@v0", "http-stream@v0"],
    variants: [
      variant("reqresp", ["http-reqresp@v0"]),
      variant("stream", ["http-stream@v0"]),
    ],
  };

  assert.equal(modelSupportsStream(model), true);
  assert.equal(modelSupportsReqresp(model), true);
});

test("variant selection prefers the currently selected compatible variant", () => {
  const variants = [
    variant("reqresp", ["http-reqresp@v0"]),
    variant("stream", ["http-stream@v0"]),
  ];

  const selected = selectVariantForInteractionMode(variants, "stream", "http-stream@v0");
  assert.equal(selected?.selection_key, "stream");
});

test("variant selection falls back to the first compatible variant for the requested mode", () => {
  const variants = [
    variant("reqresp", ["http-reqresp@v0"]),
    variant("stream", ["http-stream@v0"]),
  ];

  const selected = selectVariantForInteractionMode(variants, "reqresp", "http-stream@v0");
  assert.equal(selected?.selection_key, "stream");
});

test("variant selection returns null when no variant supports the requested mode", () => {
  const variants = [variant("reqresp", ["http-reqresp@v0"])];

  const selected = selectVariantForInteractionMode(variants, "reqresp", "http-stream@v0");
  assert.equal(selected, null);
});

test("selector headers are derived from the selected variant metadata", () => {
  const headers = selectorHeadersForVariant({
    selection_key: "stream",
    supportedModes: ["http-stream@v0"],
    extra: { provider: "vllm" },
    constraints: { gpu: "5090", tier: "standard" },
    pricePerWorkUnitWei: "25000000",
  });

  assert.deepEqual(headers, {
    "Livepeer-Selector-Constraints": '{"gpu":"5090","tier":"standard"}',
    "Livepeer-Selector-Extra": '{"provider":"vllm"}',
    "Livepeer-Selector-Max-Price-Wei": "25000000",
  });
});
