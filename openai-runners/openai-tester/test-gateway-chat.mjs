import OpenAI from "openai";

const baseURL = process.env.OPENAI_BASE_URL;
const apiKey = process.env.OPENAI_API_KEY;
const model = process.env.MODEL || "gpt-4.1-mini";

if (!baseURL) {
  console.error("Missing OPENAI_BASE_URL");
  process.exit(1);
}

if (!apiKey) {
  console.error("Missing OPENAI_API_KEY");
  process.exit(1);
}

const client = new OpenAI({ apiKey, baseURL });

console.log("Gateway SDK smoke test");
console.log(`baseURL=${baseURL}`);
console.log(`model=${model}`);

const response = await client.chat.completions.create({
  model,
  messages: [
    {
      role: "user",
      content: "Reply with exactly: gateway ok",
    },
  ],
  temperature: 0,
  max_tokens: 20,
});

const text = response.choices?.[0]?.message?.content ?? "";

console.log("raw response:");
console.log(JSON.stringify(response, null, 2));
console.log("completion:");
console.log(text);
console.log("usage:");
console.log(JSON.stringify(response.usage ?? null, null, 2));

if (!response.id || !Array.isArray(response.choices) || response.choices.length === 0) {
  console.error("Missing completion choices");
  process.exit(1);
}

console.log("PASS");
