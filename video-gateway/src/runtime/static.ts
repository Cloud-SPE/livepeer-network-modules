import { existsSync } from "node:fs";
import { resolve } from "node:path";

import fastifyStatic from "@fastify/static";
import type { FastifyInstance } from "fastify";

export async function registerSpaStatic(
  app: FastifyInstance,
  options: {
    rootDir: string;
    prefix: string;
    label: string;
  },
): Promise<void> {
  if (!existsSync(options.rootDir)) {
    app.log.warn({ rootDir: options.rootDir }, `${options.label}: dist not found, skipping static mount`);
    return;
  }

  const prefix = ensureTrailingSlash(options.prefix);
  await app.register(fastifyStatic, {
    root: options.rootDir,
    prefix,
    decorateReply: false,
    index: ["index.html"],
  });
}

export function defaultAdminDist(): string {
  return resolve(process.cwd(), "src", "frontend", "admin", "dist");
}

export function defaultPortalDist(): string {
  return resolve(process.cwd(), "src", "frontend", "portal", "dist");
}

function ensureTrailingSlash(prefix: string): string {
  return prefix.endsWith("/") ? prefix : `${prefix}/`;
}
