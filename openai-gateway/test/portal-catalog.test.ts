import test from "node:test";
import assert from "node:assert/strict";

import { Capability } from "../src/livepeer/capabilityMap.js";
import { buildPortalModelCatalog } from "../src/service/catalog.js";

test("portal catalog groups multiple offerings under one public model and unions modes", () => {
  const catalog = buildPortalModelCatalog([
    {
      capability: Capability.ChatCompletions,
      offering: "vllm-qwen3.6-27b-default",
      interactionMode: "http-reqresp@v0",
      brokerUrl: "https://broker-a.test",
      ethAddress: "0xabc",
      pricePerWorkUnitWei: "25",
      workUnit: "tokens",
      extra: { provider: "vllm", interaction_mode: "http-reqresp@v0" },
      constraints: { gpu: "5090", tier: "standard" },
      model: "Qwen3.6-27B",
    },
    {
      capability: Capability.ChatCompletions,
      offering: "vllm-qwen3.6-27b-stream",
      interactionMode: "http-stream@v0",
      brokerUrl: "https://broker-a.test",
      ethAddress: "0xabc",
      pricePerWorkUnitWei: "25",
      workUnit: "tokens",
      extra: { provider: "vllm", interaction_mode: "http-stream@v0" },
      constraints: { gpu: "5090", tier: "standard" },
      model: "Qwen3.6-27B",
    },
  ]);

  assert.equal(catalog.capabilities.length, 1);
  const capability = catalog.capabilities[0]!;
  assert.equal(capability.id, Capability.ChatCompletions);
  assert.equal(capability.models.length, 1);

  const model = capability.models[0]!;
  assert.equal(model.model_id, "Qwen3.6-27B");
  assert.deepEqual(model.supported_modes, ["http-reqresp@v0", "http-stream@v0"]);
  assert.ok(model.surface);
  assert.equal(model.variants.length, 2);
  assert.deepEqual(
    model.variants.map((variant) => variant.offering),
    ["vllm-qwen3.6-27b-default", "vllm-qwen3.6-27b-stream"],
  );
  assert.deepEqual(
    model.variants.map((variant) => variant.selection_key),
    [
      "openai:chat-completions|Qwen3.6-27B|vllm-qwen3.6-27b-default|https://broker-a.test",
      "openai:chat-completions|Qwen3.6-27B|vllm-qwen3.6-27b-stream|https://broker-a.test",
    ],
  );
});

test("portal catalog keeps separate model entries for separate public models", () => {
  const catalog = buildPortalModelCatalog([
    {
      capability: Capability.Embeddings,
      offering: "embed-small-a",
      interactionMode: "http-reqresp@v0",
      brokerUrl: "https://broker-a.test",
      ethAddress: "0xaaa",
      pricePerWorkUnitWei: "1",
      workUnit: "tokens",
      extra: { provider: "embedder-a" },
      constraints: { tier: "standard" },
      model: "embed-small-a",
    },
    {
      capability: Capability.Embeddings,
      offering: "embed-small-b",
      interactionMode: "http-reqresp@v0",
      brokerUrl: "https://broker-b.test",
      ethAddress: "0xbbb",
      pricePerWorkUnitWei: "2",
      workUnit: "tokens",
      extra: { provider: "embedder-b" },
      constraints: { tier: "premium" },
      model: "embed-small-b",
    },
  ]);

  assert.equal(catalog.capabilities.length, 1);
  const models = catalog.capabilities[0]!.models;
  assert.deepEqual(models.map((model) => model.model_id), ["embed-small-a", "embed-small-b"]);
});
