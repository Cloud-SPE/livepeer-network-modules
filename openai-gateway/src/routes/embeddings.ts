import type { FastifyInstance, FastifyReply, FastifyRequest } from "fastify";

import { Capability } from "../livepeer/capabilityMap.js";
import { LivepeerBrokerError } from "../livepeer/errors.js";
import { readOrSynthRequestId } from "../livepeer/requestId.js";
import { HEADER } from "../livepeer/headers.js";
import { resolveDefaultOffering } from "../service/offerings.js";
import { dispatchReqresp } from "../service/routeDispatch.js";
import type { RouteSelector } from "../service/routeSelector.js";
import type { Config } from "../config.js";

interface EmbeddingsBody {
  model?: string;
  input?: unknown;
  [k: string]: unknown;
}

export function registerEmbeddings(
  app: FastifyInstance,
  cfg: Config,
  routeSelector: RouteSelector,
): void {
  app.post("/v1/embeddings", async (req: FastifyRequest, reply: FastifyReply) => {
    const body = (req.body ?? {}) as EmbeddingsBody;
    const capability = Capability.Embeddings;
    const offering =
      (typeof body.model === "string" && body.model.length > 0 ? body.model : null) ??
      resolveDefaultOffering(cfg.offerings, { capability }) ??
      cfg.defaultOffering;
    const requestId = readOrSynthRequestId(req);

    try {
      const result = await dispatchReqresp({
        routeSelector,
        request: req,
        capability,
        offering,
        body: JSON.stringify(body),
        contentType: "application/json",
        requestId,
      });
      await reply
        .code(result.status)
        .header("Content-Type", result.headers.get("Content-Type") ?? "application/json")
        .header(HEADER.REQUEST_ID, requestId)
        .send(Buffer.from(result.body));
    } catch (err) {
      if (err instanceof LivepeerBrokerError) {
        await reply
          .code(err.status >= 500 ? 502 : err.status)
          .header(HEADER.REQUEST_ID, requestId)
          .send({ error: err.code, message: err.message });
        return;
      }
      await reply
        .code(500)
        .header(HEADER.REQUEST_ID, requestId)
        .send({ error: "internal_error", message: (err as Error).message });
    }
  });
}
