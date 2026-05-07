import Fastify from "fastify";
import type { FastifyInstance } from "fastify";

import type { Config } from "./config.js";

export function buildServer(cfg: Config): FastifyInstance {
  const app = Fastify({
    logger: { level: cfg.logLevel },
    bodyLimit: 16 * 1024 * 1024,
  });

  app.get("/healthz", async (_req, reply) => {
    await reply.code(200).header("Content-Type", "text/plain").send("ok\n");
  });

  return app;
}
