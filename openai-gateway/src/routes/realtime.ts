import type { FastifyInstance, FastifyPluginAsync, FastifyRequest } from "fastify";
import websocketPlugin from "@fastify/websocket";
import type { WebSocket as CustomerSocket } from "ws";

import { modes } from "@tztcloud/livepeer-gateway-middleware";

import { Capability } from "../livepeer/capabilityMap.js";
import { LivepeerBrokerError } from "../livepeer/errors.js";
import { buildPayment } from "../livepeer/payment.js";
import { readOrSynthRequestId } from "../livepeer/requestId.js";
import { resolveDefaultOffering } from "../service/offerings.js";
import { selectRealtimeCandidate } from "../service/routeDispatch.js";
import type { RouteSelector } from "../service/routeSelector.js";
import type { Config } from "../config.js";

export const registerRealtime: FastifyPluginAsync<{ cfg: Config; routeSelector: RouteSelector }> = async (
  app: FastifyInstance,
  deps: { cfg: Config; routeSelector: RouteSelector },
) => {
  await app.register(websocketPlugin);

  app.get(
    "/v1/realtime",
    { websocket: true },
    async (customerSocket: CustomerSocket, req: FastifyRequest) => {
      const cfg = deps.cfg;
      const capability = Capability.Realtime;
      const requestedModel = readModelFromQuery(req);
      const offering =
        requestedModel ??
        resolveDefaultOffering(cfg.offerings, { capability, variant: "default" }) ??
        cfg.defaultOffering;

      const requestId = readOrSynthRequestId(req);

      try {
        const candidate = await selectRealtimeCandidate(
          deps.routeSelector,
          req,
          capability,
          offering,
        );
        const paymentBlob = await buildPayment({
          capabilityId: capability,
          offeringId: candidate.offering,
        });

        const broker = await modes.wsRealtime.connect(
          { url: candidate.brokerUrl },
          {
            capability,
            offering: candidate.offering,
            paymentBlob,
            requestId,
          },
        );

        broker.onMessage((data, isBinary) => {
          customerSocket.send(data, { binary: isBinary });
        });
        broker.onClose((code, reason) => {
          if (customerSocket.readyState === customerSocket.OPEN) {
            customerSocket.close(code, reason);
          }
        });

        customerSocket.on("message", (data, isBinary) => {
          broker.send(isBinary ? (data as Buffer) : data.toString());
        });
        customerSocket.on("close", (code, reason) => {
          broker.close(code, reason?.toString());
        });
      } catch (err) {
        const closeCode = err instanceof LivepeerBrokerError
          ? err.status >= 500 ? 1011 : 1008
          : 1011;
        if (customerSocket.readyState === customerSocket.OPEN) {
          customerSocket.close(closeCode, errorMessage(err));
        }
        req.log.warn(
          { err, requestId, capability, offering, mode: modes.wsRealtime.MODE },
          "realtime: failed to open broker leg",
        );
      }
    },
  );
};

function readModelFromQuery(req: FastifyRequest): string | null {
  const q = req.query as Record<string, string | string[] | undefined> | undefined;
  if (!q) return null;
  const m = q["model"];
  if (typeof m === "string" && m.length > 0) return m;
  if (Array.isArray(m) && m.length > 0 && m[0]) return m[0];
  return null;
}

function errorMessage(err: unknown): string {
  if (err instanceof LivepeerBrokerError) return err.code;
  return "internal_error";
}
