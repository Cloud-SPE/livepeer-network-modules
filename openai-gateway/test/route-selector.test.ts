import test from "node:test";
import assert from "node:assert/strict";

import { collectResolvedResults } from "../src/service/routeSelector.js";

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
