import Fastify from "fastify";
import type { FastifyInstance } from "fastify";

import type { Config } from "./config.js";
import { registerLiveStreams } from "./routes/live-streams.js";
import { registerUploads } from "./routes/uploads.js";
import { registerVod } from "./routes/vod.js";
import { registerPlayback } from "./routes/playback.js";
import { registerProjects } from "./routes/projects.js";
import { registerWebhooks } from "./routes/webhooks.js";

export interface BuildServerInput {
  cfg: Config;
}

export function buildServer(input: BuildServerInput): FastifyInstance {
  const { cfg } = input;
  const app = Fastify({
    logger: { level: process.env["LOG_LEVEL"] ?? "info" },
    bodyLimit: 100 * 1024 * 1024,
  });

  app.get("/healthz", async (_req, reply) => {
    await reply.code(200).header("Content-Type", "text/plain").send("ok\n");
  });

  registerLiveStreams(app, { cfg });
  registerUploads(app, { cfg });
  registerVod(app);
  registerPlayback(app, { cfg });
  registerProjects(app);
  registerWebhooks(app);

  return app;
}
