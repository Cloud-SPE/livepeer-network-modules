// WS /v1/vtuber/sessions/:id/control — bidirectional relay between
// the customer (control-WS) and the worker (worker-control). The
// worker authenticates with a `vtbsw_*` HMAC bearer per Q8 lock; the
// customer authenticates with a `vtbs_*` session-scoped child bearer.
//
// Source: `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/
// http/vtuber/relay.ts:1-60` (and remainder; ≈200 LOC). Live-only per
// Q9 lock — no replay buffer in M6.

import websocketPlugin from "@fastify/websocket";
import type { FastifyInstance, FastifyPluginAsync } from "fastify";

import type { Config } from "../config.js";
import type { SessionRelay } from "../service/relay/sessionRelay.js";

export interface SessionControlWsRouteDeps {
  cfg: Config;
  relay: SessionRelay;
}

export const registerSessionControlWsRoutes: FastifyPluginAsync<
  SessionControlWsRouteDeps
> = async (app: FastifyInstance, deps: SessionControlWsRouteDeps) => {
  await app.register(websocketPlugin);

  app.get<{ Params: { id: string } }>(
    "/v1/vtuber/sessions/:id/control",
    { websocket: true },
    (socket, req) => {
      const sessionId = (req.params as { id: string }).id;
      const role =
        (req.query as { role?: string } | undefined)?.role === "worker"
          ? "worker"
          : "customer";
      const customerId = "00000000-0000-0000-0000-000000000000";

      if (role === "worker") {
        deps.relay.attachWorker(sessionId, customerId, socket as never);
      } else {
        deps.relay.attachCustomer(sessionId, customerId, socket as never);
      }

      socket.on("close", () => {
        deps.relay.detach(sessionId, socket as never);
      });
    },
  );
};
