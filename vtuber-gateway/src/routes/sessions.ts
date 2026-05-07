// POST /v1/vtuber/sessions       — session-open
// GET  /v1/vtuber/sessions/:id   — status
// POST /v1/vtuber/sessions/:id/end   — customer kill switch
// POST /v1/vtuber/sessions/:id/topup — extend the per-session ticket
//
// Source: `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/
// http/vtuber/sessions.ts:1-80` (and remainder; ≈350 LOC). Routes are
// scaffolded here with handler stubs; deep integration with the
// payment-daemon, registry, and relay state lands in the runtime
// composition root once Phase 4's wiring of those providers ships.

import type { FastifyInstance, FastifyPluginAsync } from "fastify";
import { z } from "zod";

import type { Config } from "../config.js";
import {
  SessionEndRequestSchema,
  SessionOpenRequestSchema,
  SessionTopupRequestSchema,
} from "../types/vtuber.js";

export const SessionIdParamsSchema = z.object({
  id: z.string().uuid(),
});

export interface SessionsRouteDeps {
  cfg: Config;
}

export const registerSessionsRoutes: FastifyPluginAsync<SessionsRouteDeps> =
  async (app: FastifyInstance, deps: SessionsRouteDeps) => {
    void deps;

    app.post("/v1/vtuber/sessions", async (req, reply) => {
      const parsed = SessionOpenRequestSchema.safeParse(req.body);
      if (!parsed.success) {
        await reply
          .code(400)
          .send({ error: "invalid_request", details: parsed.error.issues });
        return;
      }
      await reply.code(503).send({ error: "session_open_unimplemented" });
    });

    app.get<{ Params: { id: string } }>(
      "/v1/vtuber/sessions/:id",
      async (req, reply) => {
        const parsed = SessionIdParamsSchema.safeParse(req.params);
        if (!parsed.success) {
          await reply.code(400).send({ error: "invalid_session_id" });
          return;
        }
        await reply.code(404).send({ error: "session_not_found" });
      },
    );

    app.post<{ Params: { id: string }; Body: unknown }>(
      "/v1/vtuber/sessions/:id/end",
      async (req, reply) => {
        const parsedId = SessionIdParamsSchema.safeParse(req.params);
        if (!parsedId.success) {
          await reply.code(400).send({ error: "invalid_session_id" });
          return;
        }
        const parsedBody = SessionEndRequestSchema.safeParse(req.body ?? {});
        if (!parsedBody.success) {
          await reply.code(400).send({ error: "invalid_end_body" });
          return;
        }
        await reply.code(503).send({ error: "session_end_unimplemented" });
      },
    );

    app.post<{ Params: { id: string }; Body: unknown }>(
      "/v1/vtuber/sessions/:id/topup",
      async (req, reply) => {
        const parsedId = SessionIdParamsSchema.safeParse(req.params);
        if (!parsedId.success) {
          await reply.code(400).send({ error: "invalid_session_id" });
          return;
        }
        const parsedBody = SessionTopupRequestSchema.safeParse(req.body);
        if (!parsedBody.success) {
          await reply
            .code(400)
            .send({ error: "invalid_topup_body", details: parsedBody.error.issues });
          return;
        }
        await reply.code(503).send({ error: "session_topup_unimplemented" });
      },
    );
  };
