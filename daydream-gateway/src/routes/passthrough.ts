/**
 * Scope-API-compatible passthrough.
 *
 * Forwards every /api/v1/* request to the chosen orch's broker
 * /_scope/<session_id>/* reverse proxy. The session id is derived from
 * either:
 *   - the `X-Daydream-Session` header, or
 *   - the `session_id` query parameter, or
 *   - falling back to a singleton-session shortcut (most recent open).
 *
 * The session-start path (`/api/v1/session/start`) is special: it is
 * the moment the broker's seconds-elapsed clock anchors at the backend.
 * The gateway's session-open already happened at POST /v1/sessions; the
 * proxy short-circuit on the broker handles starting the clock.
 */

import type { FastifyInstance, FastifyRequest } from "fastify";

import type { SessionRouter, SessionRecord } from "../sessionRouter.js";

export function registerPassthroughRoutes(
  app: FastifyInstance,
  router: SessionRouter,
): void {
  // Match any method, any /api/v1/* path. Fastify "*" wildcard for path.
  app.route({
    method: ["GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"],
    url: "/api/v1/*",
    handler: async (req, reply) => {
      const rec = resolveSession(req, router);
      if (!rec) {
        return reply.code(409).send({
          error: "no_session",
          message:
            "no active session; POST /v1/sessions first, then re-send with X-Daydream-Session: <session_id>",
        });
      }

      const upstreamPath = (req.params as { "*": string })["*"] ?? "";
      const upstreamURL =
        stripTrailingSlash(rec.scopeUrl) +
        "/api/v1/" +
        upstreamPath +
        (req.url.includes("?") ? req.url.slice(req.url.indexOf("?")) : "");

      const headers: Record<string, string> = {};
      for (const [k, v] of Object.entries(req.headers)) {
        if (v === undefined) continue;
        const key = k.toLowerCase();
        if (
          key === "host" ||
          key === "content-length" ||
          key === "connection"
        ) {
          continue;
        }
        headers[k] = Array.isArray(v) ? v.join(", ") : String(v);
      }

      const init: RequestInit = {
        method: req.method,
        headers,
      };
      if (req.method !== "GET" && req.method !== "HEAD") {
        // Fastify's rawBody is undefined unless a content-type parser
        // captured it; for our pass-through we re-stringify the body
        // (Scope's API accepts JSON for the calls SPA makes).
        init.body = req.body
          ? typeof req.body === "string"
            ? req.body
            : JSON.stringify(req.body)
          : undefined;
      }

      let res: Response;
      try {
        res = await fetch(upstreamURL, init);
      } catch (e) {
        return reply
          .code(502)
          .send({ error: "upstream_unreachable", message: (e as Error).message });
      }

      reply.code(res.status);
      res.headers.forEach((value, key) => {
        // Don't forward transfer-encoding; let Fastify recompute.
        if (key.toLowerCase() === "transfer-encoding") return;
        reply.header(key, value);
      });
      const buf = Buffer.from(await res.arrayBuffer());
      return reply.send(buf);
    },
  });
}

function resolveSession(
  req: FastifyRequest,
  router: SessionRouter,
): SessionRecord | undefined {
  const explicit =
    (req.headers["x-daydream-session"] as string | undefined) ??
    ((req.query as { session_id?: string }).session_id);
  if (explicit) {
    return router.get(explicit);
  }
  // Singleton shortcut: most recent session. Useful when a SPA is the
  // sole consumer and only ever has one session in flight.
  const all = router.list();
  if (all.length === 0) return undefined;
  return all.reduce((a, b) => (a.createdAt > b.createdAt ? a : b));
}

function stripTrailingSlash(s: string): string {
  return s.endsWith("/") ? s.slice(0, -1) : s;
}
