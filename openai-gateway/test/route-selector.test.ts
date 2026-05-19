import test from "node:test";
import assert from "node:assert/strict";

import { collectResolvedResults, flattenResolveResult } from "../src/service/routeSelector.js";

test("collectResolvedResults keeps fulfilled resolver results and skips rejected entries", () => {
  const results = collectResolvedResults([
    {
      status: "fulfilled",
      value: {
        nodes: [
          {
            url: "https://broker-a.example.com",
            operatorAddress: "0xaaa",
            enabled: true,
            capabilities: [],
          },
        ],
      },
    },
    {
      status: "rejected",
      reason: new Error("5 NOT_FOUND: not_found"),
    },
    {
      status: "fulfilled",
      value: {
        nodes: [
          {
            url: "https://broker-b.example.com",
            operatorAddress: "0xbbb",
            enabled: true,
            capabilities: [],
          },
        ],
      },
    },
  ]);

  assert.equal(results.length, 2);
  assert.equal(results[0]?.nodes[0]?.operatorAddress, "0xaaa");
  assert.equal(results[1]?.nodes[0]?.operatorAddress, "0xbbb");
});

test("flattenResolveResult preserves exact resolver capability IDs", () => {
  const candidates = flattenResolveResult({
    nodes: [
      {
        url: "https://broker.example.com",
        operatorAddress: "0xabc",
        enabled: true,
        capabilities: [
          {
            name: "openai:/v1/chat/completions",
            workUnit: "tokens",
            offerings: [{ id: "gpt-oss-20b", pricePerWorkUnitWei: "1000" }],
          },
          {
            name: "openai:chat-completions",
            workUnit: "tokens",
            extraJson: JSON.stringify({ openai: { model: "qwen3.6-27b" } }),
            offerings: [{ id: "vllm-qwen3.6-27b-default", pricePerWorkUnitWei: "2000" }],
          },
        ],
      },
    ],
  });

  assert.deepEqual(
    candidates.map((candidate) => ({
      capability: candidate.capability,
      offering: candidate.offering,
      model: candidate.model,
    })),
    [
      {
        capability: "openai:/v1/chat/completions",
        offering: "gpt-oss-20b",
        model: null,
      },
      {
        capability: "openai:chat-completions",
        offering: "vllm-qwen3.6-27b-default",
        model: "qwen3.6-27b",
      },
    ],
  );
});
