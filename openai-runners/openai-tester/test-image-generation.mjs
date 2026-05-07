#!/usr/bin/env node

/**
 * Test image generation via the OpenAI-compatible proxy.
 *
 * Usage:
 *   OPENAI_BASE_URL="http://localhost:8090/v1" MODEL="SG161222/RealVisXL_V4.0_Lightning" node test-image-generation.mjs
 *
 * Environment:
 *   OPENAI_BASE_URL  - proxy base URL (default: http://localhost:8090/v1)
 *   MODEL            - model name (default: black-forest-labs/FLUX.1-dev or supports: SG161222/RealVisXL_V4.0_Lightning)
 *   PROMPT           - image prompt (default: "A cat wearing a top hat, digital art")
 *   SIZE             - image size (default: 1024x1024)
 */

import OpenAI from "openai";
import { writeFileSync } from "node:fs";

const client = new OpenAI({
  baseURL: process.env.OPENAI_BASE_URL || "http://localhost:8090/v1",
  apiKey: "not-needed", // proxy strips auth
});

const model = process.env.MODEL || "black-forest-labs/FLUX.1-dev";
const prompt =
  process.env.PROMPT || "A cat wearing a top hat, digital art, high quality";
const size = process.env.SIZE || "1024x1024";

console.log(`Generating image...`);
console.log(`  Model:  ${model}`);
console.log(`  Prompt: ${prompt}`);
console.log(`  Size:   ${size}`);
console.log();

try {
  const response = await client.images.generate({
    model,
    prompt,
    n: 2,
    size,
  });

  // The OpenAI SDK may return a raw string if the upstream server
  // doesn't set Content-Type: application/json. Handle both cases.
  const parsed = typeof response === "string" ? JSON.parse(response) : response;
  const images = parsed.data ?? [];

  console.log(`✅ Success! ${images.length} image(s) generated`);
  for (let i = 0; i < images.length; i++) {
    const img = images[i];
    const filename = `output${images.length > 1 ? `-${i + 1}` : ""}.png`;

    if (img.b64_json) {
      writeFileSync(filename, Buffer.from(img.b64_json, "base64"));
      console.log(`  Saved: ${filename} (${(Buffer.from(img.b64_json, "base64").length / 1024).toFixed(0)} KB)`);
    } else if (img.url) {
      console.log(`  URL: ${img.url}`);
    }
  }

  if (images.length === 0) {
    console.error(`⚠️  No images in response. Full response:`);
    console.error(JSON.stringify(parsed, null, 2));
    process.exit(1);
  }
} catch (err) {
  console.error(`❌ Error: ${err.message}`);
  if (err.status) {
    console.error(`  Status: ${err.status}`);
  }
  if (err.error) {
    console.error(`  Body: ${JSON.stringify(err.error)}`);
  }
  process.exit(1);
}
