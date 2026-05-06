import Fastify from "fastify";
import type { FastifyInstance } from "fastify";

import type { Config } from "./config.js";
import { registerChatCompletions } from "./routes/chat-completions.js";
import { registerEmbeddings } from "./routes/embeddings.js";
import { registerAudioTranscriptions } from "./routes/audio-transcriptions.js";

export function buildServer(cfg: Config): FastifyInstance {
  const app = Fastify({
    logger: { level: process.env["LOG_LEVEL"] ?? "info" },
    // Accept large bodies for image/audio uploads.
    bodyLimit: 100 * 1024 * 1024,
  });

  // Accept multipart/form-data bodies as raw buffers (we forward them
  // verbatim to the broker; Fastify's default body-parser registry
  // rejects unknown content-types with 415).
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

  return app;
}
