// Live AI stream entry point. Replaces the old daydreamlive/livepeer
// WHIP gateway flow: the portal SPA POSTs here with a UI token; we
// authenticate the caller, open a session on daydream-gateway, persist
// a usage_events row, and return the scope_url. The browser then opens
// WebRTC directly against the orchestrator-served scope_url — no media
// touches this portal.

import type { FastifyInstance } from "fastify";
import { z } from "zod";
import type { NodePgDatabase } from "drizzle-orm/node-postgres";
import type { CustomerPortal } from "@livepeer-network-modules/customer-portal";
import type { GatewayClient } from "../service/gatewayClient.js";
import {
  closeSessionUsage,
  openSessionUsage,
} from "../repo/usage.js";
import { customerAuthPreHandler } from "../middleware/auth.js";

const OpenSessionSchema = z.object({
  params: z.record(z.string(), z.unknown()).optional(),
});

export interface RegisterSessionRoutesDeps {
  db: NodePgDatabase<Record<string, unknown>>;
  portal: CustomerPortal;
  gateway: GatewayClient;
  capability: string;
}

export function registerSessionRoutes(
  app: FastifyInstance,
  deps: RegisterSessionRoutesDeps,
): void {
  const requireCustomer = customerAuthPreHandler(deps.portal.customerTokenService);

  app.post(
    "/portal/sessions",
    { preHandler: requireCustomer },
    async (req, reply) => {
      const parsed = OpenSessionSchema.safeParse(req.body ?? {});
      if (!parsed.success) {
        await reply
          .code(400)
          .send({ error: "invalid_request", details: parsed.error.flatten() });
        return;
      }
      const session = req.customerSession!;
      const opened = await deps.gateway.openSession({
        capability: deps.capability,
        params: parsed.data.params,
      });
      await openSessionUsage(deps.db, {
        customerId: session.customer.id,
        sessionId: opened.sessionId,
        orchestrator: opened.orchestrator,
      });
      await reply.send({
        session_id: opened.sessionId,
        scope_url: opened.scopeUrl,
        orchestrator: opened.orchestrator,
      });
    },
  );

  // SPA fires this when WebRTC tear-down completes (page unload,
  // explicit Stop, or error). Idempotent: if the row is already
  // closed we just no-op.
  app.post<{ Params: { id: string } }>(
    "/portal/sessions/:id/close",
    { preHandler: requireCustomer },
    async (req, reply) => {
      const sessionId = req.params.id;
      try {
        await deps.gateway.closeSession(sessionId);
      } catch (err) {
        req.log.warn({ err, sessionId }, "gateway close failed; continuing");
      }
      const row = await closeSessionUsage(deps.db, { sessionId });
      await reply.send({
        session_id: sessionId,
        closed: row !== null,
        duration_seconds: row?.durationSeconds ?? null,
      });
    },
  );
}
