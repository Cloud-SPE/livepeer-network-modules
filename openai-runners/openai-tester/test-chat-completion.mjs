import OpenAI from "openai";

const baseURL = process.env.OPENAI_BASE_URL || "http://localhost:8090/v1";
const apiKey = process.env.OPENAI_API_KEY || "local-dev-no-auth";
const model  =process.env.MODEL || "llama3.1:8b"
const client = new OpenAI({ apiKey, baseURL });

// --- Non-streaming test ---

console.log(`=== Non-streaming chat completion ${model}=== \n`);

const resp = await client.chat.completions.create({
  model,
  stream: false,
  messages: [
    { role: "user", content: "Say hello in one sentence, then give one fun fact about octopuses." }
  ]
});

console.log(JSON.stringify(resp, null, 2));

// --- Streaming test ---

console.log("\n=== Streaming chat completion ===\n");

const stream = await client.chat.completions.create({
  model: process.env.MODEL || "llama3.1:8b",
  stream: true,
  messages: [
    { role: "user", content: "Count from 1 to 10 slowly, one number per short phrase." }
  ]
});

for await (const part of stream) {
  const delta = part.choices?.[0]?.delta?.content ?? "";
  process.stdout.write(delta);
}
process.stdout.write("\n");
