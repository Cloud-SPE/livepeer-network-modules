import { randomUUID } from "node:crypto";
import type { FastifyRequest } from "fastify";

import { HEADER } from "./headers.js";

export function readOrSynthRequestId(req: FastifyRequest): string {
  const h = req.headers[HEADER.REQUEST_ID.toLowerCase()];
  if (typeof h === "string" && h.length > 0) return h;
  if (Array.isArray(h) && h.length > 0 && h[0]) return h[0];
  return randomUUID();
}
