import websocketPlugin from "@fastify/websocket";
import type { FastifyInstance, FastifyPluginAsync } from "fastify";

import type { Config } from "../config.js";
import type { ReconnectWindow } from "../service/relay/reconnectWindow.js";
import { parseLastSeqHeader } from "../service/relay/reconnectWindow.js";
import type { SessionRelay } from "../service/relay/sessionRelay.js";

export interface SessionControlWsRouteDeps {
  cfg: Config;
  relay: SessionRelay;
  reconnectWindow?: ReconnectWindow;
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
        socket.on("close", () => {
          deps.relay.detach(sessionId, socket as never);
        });
        return;
      }

      if (deps.reconnectWindow !== undefined) {
        deps.reconnectWindow.registerSession(sessionId);
        const lastSeq = parseLastSeqHeader(req.headers["last-seq"]);
        const result = deps.reconnectWindow.attachCustomer(sessionId, lastSeq);
        if (result.kind === "conflict") {
          try {
            socket.close(1008, result.reason);
          } catch {
            // best-effort
          }
          return;
        }
        for (const entry of result.replay) {
          try {
            socket.send(entry.payload);
          } catch {
            // best-effort
          }
        }
      }

      deps.relay.attachCustomer(sessionId, customerId, socket as never);

      socket.on("close", () => {
        deps.relay.detach(sessionId, socket as never);
        if (deps.reconnectWindow !== undefined) {
          deps.reconnectWindow.detachCustomer(sessionId);
        }
      });
    },
  );
};
