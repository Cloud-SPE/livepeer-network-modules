import Fastify from "fastify";
import type { FastifyInstance } from "fastify";

import type { Config } from "./config.js";

export interface BuildServerInput {
  cfg: Config;
}

export function buildServer(_input: BuildServerInput): FastifyInstance {
  const app = Fastify({
    logger: { level: process.env["LOG_LEVEL"] ?? "info" },
    bodyLimit: 100 * 1024 * 1024,
  });

  app.get("/healthz", async (_req, reply) => {
    await reply.code(200).header("Content-Type", "text/plain").send("ok\n");
  });

  return app;
}
