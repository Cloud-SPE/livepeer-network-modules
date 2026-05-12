#!/usr/bin/env node

/**
 * Test audio transcription via the OpenAI-compatible proxy.
 *
 * Usage:
 *   OPENAI_BASE_URL="http://localhost:8090/v1" MODEL="whisper-large-v3" AUDIO_FILE="sample.mp3" \
 *     node test-audio-transcription.mjs
 *
 * Environment:
 *   OPENAI_BASE_URL  - proxy base URL (default: http://localhost:8090/v1)
 *   MODEL            - model name (default: whisper-large-v3)
 *   AUDIO_FILE       - path to audio file (default: ./sample.mp3)
 *   RESPONSE_FORMAT  - json | text | srt | vtt | verbose_json (default: json)
 */

import fs from "node:fs";
import path from "node:path";
import OpenAI from "openai";

const client = new OpenAI({
  baseURL: process.env.OPENAI_BASE_URL || "http://localhost:8090/v1",
  apiKey: process.env.OPENAI_API_KEY || "local-dev-no-auth",
});

const model = process.env.MODEL || "whisper-large-v3";
const audioPath = process.env.AUDIO_FILE || "sample.mp3";
const responseFormat = process.env.RESPONSE_FORMAT || "json";

if (!fs.existsSync(audioPath)) {
  console.error(`Error: audio file not found at ${path.resolve(audioPath)}`);
  process.exit(1);
}

console.log(`Transcribing audio...`);
console.log(`  Model:           ${model}`);
console.log(`  File:            ${audioPath}`);
console.log(`  Response format: ${responseFormat}`);
console.log();

try {
  const response = await client.audio.transcriptions.create({
    model,
    file: fs.createReadStream(audioPath),
    response_format: responseFormat,
  });

  if (responseFormat === "json" || responseFormat === "verbose_json") {
    console.log(`Success:`);
    console.log(JSON.stringify(response, null, 2));
    if (!response.text) {
      console.error("Warning: empty text in response");
      process.exit(1);
    }
  } else {
    console.log(`Success (${responseFormat}):`);
    console.log(typeof response === "string" ? response : JSON.stringify(response));
  }
} catch (err) {
  console.error(`Error: ${err.message}`);
  if (err.status) console.error(`  Status: ${err.status}`);
  if (err.error) console.error(`  Body: ${JSON.stringify(err.error)}`);
  process.exit(1);
}
