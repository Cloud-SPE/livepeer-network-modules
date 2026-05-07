#!/usr/bin/env node

/**
 * Test embeddings via the OpenAI-compatible proxy.
 *
 * Usage:
 *   OPENAI_BASE_URL="http://localhost:8090/v1" MODEL="nomic-embed-text" node test-text-embedding.mjs
 *
 * Environment:
 *   OPENAI_BASE_URL  - proxy base URL (default: http://localhost:8090/v1)
 *   MODEL            - model name (default: nomic-embed-text)
 */

import OpenAI from "openai";

const client = new OpenAI({
  baseURL: process.env.OPENAI_BASE_URL || "http://localhost:8090/v1",
  apiKey: "not-needed", // proxy strips auth
});

const model = process.env.MODEL || "nomic-embed-text:latest";
const input = process.env.INPUT || "Hello world";

console.log(`Creating embeddings...`);
console.log(`  Model: ${model}`);
console.log(`  Input: ${input}`);
console.log();

try {
  const response = await client.embeddings.create({
    model,
    input,
  });

  const parsed = typeof response === "string" ? JSON.parse(response) : response;
  const embeddings = parsed.data ?? [];

  console.log(`Success! ${embeddings.length} embedding(s) returned`);
  for (let i = 0; i < embeddings.length; i++) {
    const emb = embeddings[i];
    const dims = emb.embedding?.length ?? 0;
    const preview = emb.embedding?.slice(0, 5).map((v) => v.toFixed(6)).join(", ");
    console.log(`  [${i}] dimensions=${dims}, first 5 values=[${preview}]`);
  }

  if (parsed.usage) {
    console.log(`  Usage: ${JSON.stringify(parsed.usage)}`);
  }

  if (embeddings.length === 0) {
    console.error(`Warning: No embeddings in response. Full response:`);
    console.error(JSON.stringify(parsed, null, 2));
    process.exit(1);
  }
} catch (err) {
  console.error(`Error: ${err.message}`);
  if (err.status) {
    console.error(`  Status: ${err.status}`);
  }
  if (err.error) {
    console.error(`  Body: ${JSON.stringify(err.error)}`);
  }
  process.exit(1);
}
