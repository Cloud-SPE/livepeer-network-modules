import type { FastifyInstance, FastifyReply, FastifyRequest } from "fastify";

import * as httpReqresp from "../livepeer/http-reqresp.js";
import * as httpStream from "../livepeer/http-stream.js";
import { Capability } from "../livepeer/capabilityMap.js";
import { LivepeerBrokerError } from "../livepeer/errors.js";
import { buildPayment } from "../livepeer/payment.js";
import { readOrSynthRequestId } from "../livepeer/requestId.js";
import { resolveDefaultOffering } from "../service/offerings.js";
import { HEADER } from "../livepeer/headers.js";
import type { Config } from "../config.js";

interface ChatCompletionsBody {
  model?: string;
  stream?: boolean;
  [k: string]: unknown;
}

export function registerChatCompletions(app: FastifyInstance, cfg: Config): void {
  app.post("/v1/chat/completions", async (req: FastifyRequest, reply: FastifyReply) => {
    const body = (req.body ?? {}) as ChatCompletionsBody;
    const isStream = body.stream === true;
    const capability = Capability.ChatCompletions;
    const variant = isStream ? "streaming" : "non-streaming";

    const offering =
      (typeof body.model === "string" && body.model.length > 0 ? body.model : null) ??
      resolveDefaultOffering(cfg.offerings, { capability, variant }) ??
      cfg.defaultOffering;

    const requestId = readOrSynthRequestId(req);
    const bodyStr = JSON.stringify(body);

    try {
      if (isStream) {
        const result = await httpStream.send({
          brokerUrl: cfg.brokerUrl,
          capability,
          offering,
          paymentBlob: await buildPayment({ capabilityId: capability, offeringId: offering }),
          body: bodyStr,
          contentType: "application/json",
          requestId,
        });
        await reply
          .code(result.status)
          .header("Content-Type", "text/event-stream")
          .header(HEADER.REQUEST_ID, requestId)
          .send(result.body);
        return;
      }

      const result = await httpReqresp.send({
        brokerUrl: cfg.brokerUrl,
        capability,
        offering,
        paymentBlob: await buildPayment({ capabilityId: capability, offeringId: offering }),
        body: bodyStr,
        contentType: "application/json",
        requestId,
      });
      await reply
        .code(result.status)
        .header("Content-Type", result.headers.get("Content-Type") ?? "application/json")
        .header(HEADER.REQUEST_ID, requestId)
        .send(Buffer.from(result.body));
    } catch (err) {
      handleBrokerError(reply, err, requestId);
    }
  });
}

function handleBrokerError(reply: FastifyReply, err: unknown, requestId: string): void {
  if (err instanceof LivepeerBrokerError) {
    void reply
      .code(err.status >= 500 ? 502 : err.status)
      .header("Content-Type", "application/json")
      .header(HEADER.REQUEST_ID, requestId)
      .send({ error: err.code, message: err.message });
    return;
  }
  void reply
    .code(500)
    .header("Content-Type", "application/json")
    .header(HEADER.REQUEST_ID, requestId)
    .send({ error: "internal_error", message: (err as Error).message });
}
