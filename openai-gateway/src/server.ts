import Fastify from "fastify";
import type { FastifyInstance } from "fastify";

import type { Config } from "./config.js";
import { registerChatCompletions } from "./routes/chat-completions.js";
import { registerEmbeddings } from "./routes/embeddings.js";
import { registerAudioTranscriptions } from "./routes/audio-transcriptions.js";
import { registerAudioSpeech } from "./routes/audio-speech.js";
import { registerImagesGenerations } from "./routes/images-generations.js";

export function buildServer(cfg: Config): FastifyInstance {
  const app = Fastify({
    logger: { level: process.env["LOG_LEVEL"] ?? "info" },
    bodyLimit: 100 * 1024 * 1024,
  });

  app.addContentTypeParser(
    /^multipart\/form-data/,
    { parseAs: "buffer" },
    (_req, body, done) => {
      done(null, body);
    },
  );

  app.get("/healthz", async (_req, reply) => {
    await reply.code(200).header("Content-Type", "text/plain").send("ok\n");
  });

  registerChatCompletions(app, cfg);
  registerEmbeddings(app, cfg);
  registerAudioTranscriptions(app, cfg);
  registerAudioSpeech(app, cfg);
  registerImagesGenerations(app, cfg);

  return app;
}
