import test from "node:test";
import assert from "node:assert/strict";

import { Capability } from "../src/livepeer/capabilityMap.js";
import { surfaceForCapability } from "../src/service/openaiSurface.js";
import { buildModelCatalog } from "../src/service/catalog.js";

test("openai surface descriptors exist for all playground-paid capabilities", () => {
  const capabilities = [
    Capability.ChatCompletions,
    Capability.Embeddings,
    Capability.ImagesGenerations,
    Capability.AudioSpeech,
    Capability.AudioTranscriptions,
  ];
  for (const capability of capabilities) {
    const surface = surfaceForCapability(capability);
    assert.ok(surface, `missing surface descriptor for ${capability}`);
    assert.ok(surface!.requestFields.length > 0, `missing request fields for ${capability}`);
    assert.ok(surface!.responseVariants.length > 0, `missing response variants for ${capability}`);
  }
});

test("model catalog entries include capability surface metadata", () => {
  const catalog = buildModelCatalog([
    {
      capability: Capability.ChatCompletions,
      offering: "model-small",
      interactionMode: "http-reqresp@v0",
      brokerUrl: "http://broker.test",
      ethAddress: "0xabc",
      pricePerWorkUnitWei: "1",
      workUnit: "tokens",
      extra: { interaction_mode: "http-reqresp@v0" },
      constraints: { tier: "standard" },
      model: "model-small",
    },
  ]);
  assert.equal(catalog.length, 1);
  assert.equal(catalog[0]?.surface?.capability, Capability.ChatCompletions);
  assert.ok((catalog[0]?.surface?.requestFields.length ?? 0) > 0);
});
