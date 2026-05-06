import type { FastifyInstance, FastifyReply, FastifyRequest } from "fastify";

import * as httpReqresp from "../livepeer/http-reqresp.js";
import { LivepeerBrokerError } from "../livepeer/errors.js";
import { buildPayment } from "../livepeer/payment.js";
import type { Config } from "../config.js";

interface EmbeddingsBody {
  model?: string;
  input?: unknown;
  [k: string]: unknown;
}

export function registerEmbeddings(app: FastifyInstance, cfg: Config): void {
  app.post("/v1/embeddings", async (req: FastifyRequest, reply: FastifyReply) => {
    const body = (req.body ?? {}) as EmbeddingsBody;
    const model = typeof body.model === "string" && body.model.length > 0 ? body.model : "default";

    const capability = `openai:embeddings:${model}`;

    try {
      const result = await httpReqresp.send({
        brokerUrl: cfg.brokerUrl,
        capability,
        offering: cfg.defaultOffering,
        paymentBlob: await buildPayment({ capabilityId: capability, offeringId: cfg.defaultOffering }),
        body: JSON.stringify(body),
        contentType: "application/json",
      });
      await reply
        .code(result.status)
        .header("Content-Type", result.headers.get("Content-Type") ?? "application/json")
        .send(Buffer.from(result.body));
    } catch (err) {
      if (err instanceof LivepeerBrokerError) {
        await reply.code(err.status >= 500 ? 502 : err.status).send({ error: err.code, message: err.message });
        return;
      }
      await reply.code(500).send({ error: "internal_error", message: (err as Error).message });
    }
  });
}
