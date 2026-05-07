import type { FastifyInstance } from "fastify";

import type { RetryDispatcher } from "../service/webhookDispatcher.js";
import type { WebhookFailureRepo } from "../repo/index.js";

export interface AdminRoutesDeps {
  failures: WebhookFailureRepo;
  dispatcher: RetryDispatcher;
}

export function registerAdmin(app: FastifyInstance, deps: AdminRoutesDeps): void {
  app.get("/admin/webhook-failures", async (req, reply) => {
    const query = req.query as Record<string, string | undefined>;
    const limit = Math.min(parseInt(query["limit"] ?? "50", 10) || 50, 200);
    const endpointId = query["endpoint_id"];
    const list = await deps.failures.list(
      endpointId ? { endpointId, limit } : { limit },
    );
    await reply.code(200).send({ items: list });
  });

  app.post("/admin/webhook-failures/:id/replay", async (req, reply) => {
    const { id } = req.params as { id: string };
    try {
      const out = await deps.dispatcher.replayFailure(id);
      await reply.code(out.delivered ? 200 : 502).send({
        delivered: out.delivered,
        attempts: out.attempts,
        final_status: out.finalStatus,
        last_error: out.lastError,
      });
    } catch (err) {
      const msg = err instanceof Error ? err.message : "replay_failed";
      await reply.code(404).send({ error: msg });
    }
  });
}
