import { existsSync } from "node:fs";
import { resolve } from "node:path";

import fastifyStatic from "@fastify/static";
import type { FastifyInstance } from "fastify";

export async function registerStudioStatic(app: FastifyInstance): Promise<void> {
  const rootDir = resolve(process.cwd(), "frontend", "dist");
  if (!existsSync(rootDir)) {
    app.log.warn({ rootDir }, "daydream-ui: dist not found, skipping static mount");
    return;
  }

  await app.register(fastifyStatic, {
    root: rootDir,
    prefix: "/",
  });

  app.get("/", async (_req, reply) => reply.sendFile("index.html"));
  app.setNotFoundHandler(async (req, reply) => {
    const path = req.url.split("?", 1)[0].replace(/^\/+/, "");
    if (
      req.method !== "GET" &&
      req.method !== "HEAD"
    ) {
      return reply.code(404).send({ error: "not_found" });
    }
    if (
      path === "healthz" ||
      path.startsWith("api/") ||
      path.startsWith("v1/") ||
      path.includes(".")
    ) {
      return reply.code(404).send({ error: "not_found" });
    }
    return reply.type("text/html; charset=utf-8").sendFile("index.html");
  });
}
