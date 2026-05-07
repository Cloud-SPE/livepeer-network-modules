#!/usr/bin/env node

/**
 * Test text-to-speech via the OpenAI-compatible proxy.
 *
 * Usage:
 *   OPENAI_BASE_URL="http://localhost:8090/v1" MODEL="kokoro" VOICE="alloy" \
 *     node test-audio-speech.mjs
 *
 * Environment:
 *   OPENAI_BASE_URL  - proxy base URL (default: http://localhost:8090/v1)
 *   MODEL            - model name (default: kokoro)
 *   VOICE            - voice name (default: alloy). Accepts OpenAI voices
 *                       (alloy/echo/fable/onyx/nova/shimmer) or Kokoro IDs
 *                       (af_bella/am_adam/bf_emma/…).
 *   INPUT            - text to synthesise (default: a short greeting)
 *   RESPONSE_FORMAT  - mp3 | opus | aac | flac | wav | pcm (default: mp3)
 *   OUT_FILE         - output path (default: ./out.<RESPONSE_FORMAT>)
 */

import fs from "node:fs";
import { Buffer } from "node:buffer";
import OpenAI from "openai";

const client = new OpenAI({
  baseURL: process.env.OPENAI_BASE_URL || "http://localhost:8090/v1",
  apiKey: "not-needed",
});

const model = process.env.MODEL || "kokoro";
const voice = process.env.VOICE || "alloy";
const input = process.env.INPUT || "Hello from the Livepeer BYOC audio speech runner.";
const responseFormat = process.env.RESPONSE_FORMAT || "mp3";
const outFile = process.env.OUT_FILE || `out.${responseFormat}`;

console.log(`Synthesising speech...`);
console.log(`  Model:           ${model}`);
console.log(`  Voice:           ${voice}`);
console.log(`  Response format: ${responseFormat}`);
console.log(`  Output file:     ${outFile}`);
console.log(`  Input:           ${JSON.stringify(input)}`);
console.log();

try {
  const response = await client.audio.speech.create({
    model,
    voice,
    input,
    response_format: responseFormat,
  });

  const buffer = Buffer.from(await response.arrayBuffer());
  fs.writeFileSync(outFile, buffer);

  if (buffer.length === 0) {
    console.error("Error: empty audio response");
    process.exit(1);
  }

  console.log(`Success! Wrote ${buffer.length} bytes to ${outFile}`);
} catch (err) {
  console.error(`Error: ${err.message}`);
  if (err.status) console.error(`  Status: ${err.status}`);
  if (err.error) console.error(`  Body: ${JSON.stringify(err.error)}`);
  process.exit(1);
}
