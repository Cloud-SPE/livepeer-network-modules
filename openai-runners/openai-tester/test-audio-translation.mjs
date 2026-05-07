#!/usr/bin/env node

/**
 * Test audio translation (non-English speech → English text) via the proxy.
 *
 * Usage:
 *   OPENAI_BASE_URL="http://localhost:8090/v1" MODEL="whisper-large-v3" AUDIO_FILE="sample.mp3" \
 *     node test-audio-translation.mjs
 *
 * Environment:
 *   OPENAI_BASE_URL  - proxy base URL (default: http://localhost:8090/v1)
 *   MODEL            - model name (default: whisper-large-v3)
 *   AUDIO_FILE       - path to audio file (default: ./sample.mp3)
 */

import fs from "node:fs";
import path from "node:path";
import OpenAI from "openai";

const client = new OpenAI({
  baseURL: process.env.OPENAI_BASE_URL || "http://localhost:8090/v1",
  apiKey: "not-needed",
});

const model = process.env.MODEL || "whisper-large-v3";
const audioPath = process.env.AUDIO_FILE || "sample.mp3";

if (!fs.existsSync(audioPath)) {
  console.error(`Error: audio file not found at ${path.resolve(audioPath)}`);
  process.exit(1);
}

console.log(`Translating audio to English...`);
console.log(`  Model: ${model}`);
console.log(`  File:  ${audioPath}`);
console.log();

try {
  const response = await client.audio.translations.create({
    model,
    file: fs.createReadStream(audioPath),
  });

  console.log(`Success:`);
  console.log(JSON.stringify(response, null, 2));
  if (!response.text) {
    console.error("Warning: empty text in response");
    process.exit(1);
  }
} catch (err) {
  console.error(`Error: ${err.message}`);
  if (err.status) console.error(`  Status: ${err.status}`);
  if (err.error) console.error(`  Body: ${JSON.stringify(err.error)}`);
  process.exit(1);
}
