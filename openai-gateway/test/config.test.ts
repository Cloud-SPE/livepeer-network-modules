import test from "node:test";
import assert from "node:assert/strict";

import { loadConfig } from "../src/config.js";

test("loadConfig accepts OPENAI_GATEWAY_ADMIN_TOKENS", () => {
  const env = withEnv({
    LIVEPEER_BROKER_URL: "https://broker.example.com",
    DATABASE_URL: "postgres://postgres:postgres@localhost:5432/openai_gateway",
    OPENAI_DEFAULT_OFFERING_PER_CAPABILITY: fixtureOfferingsPath(),
    OPENAI_GATEWAY_ADMIN_TOKENS: "token-a, token-b",
  });
  try {
    const cfg = loadConfig();
    assert.deepEqual(cfg.adminTokens, ["token-a", "token-b"]);
  } finally {
    env.restore();
  }
});

test("loadConfig returns an empty admin token list when OPENAI_GATEWAY_ADMIN_TOKENS is unset", () => {
  const env = withEnv({
    LIVEPEER_BROKER_URL: "https://broker.example.com",
    DATABASE_URL: "postgres://postgres:postgres@localhost:5432/openai_gateway",
    OPENAI_DEFAULT_OFFERING_PER_CAPABILITY: fixtureOfferingsPath(),
  });
  try {
    const cfg = loadConfig();
    assert.deepEqual(cfg.adminTokens, []);
  } finally {
    env.restore();
  }
});

function fixtureOfferingsPath(): string {
  return new URL("./fixtures/offerings.yaml", import.meta.url).pathname;
}

function withEnv(next: Record<string, string>): { restore: () => void } {
  const previous = new Map<string, string | undefined>();
  for (const key of [
    "LIVEPEER_BROKER_URL",
    "LIVEPEER_RESOLVER_SOCKET",
    "DATABASE_URL",
    "OPENAI_DEFAULT_OFFERING_PER_CAPABILITY",
    "OPENAI_GATEWAY_ADMIN_TOKENS",
  ]) {
    previous.set(key, process.env[key]);
    delete process.env[key];
  }
  for (const [key, value] of Object.entries(next)) {
    process.env[key] = value;
  }
  return {
    restore() {
      for (const [key, value] of previous.entries()) {
        if (value === undefined) delete process.env[key];
        else process.env[key] = value;
      }
    },
  };
}
