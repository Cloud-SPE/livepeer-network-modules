import type { FastifyInstance, FastifyReply, FastifyRequest } from "fastify";

import * as httpReqresp from "../livepeer/http-reqresp.js";
import * as httpStream from "../livepeer/http-stream.js";
import { LivepeerBrokerError } from "../livepeer/errors.js";
import type { Config } from "../config.js";

interface ChatCompletionsBody {
  model?: string;
  stream?: boolean;
  [k: string]: unknown;
}

export function registerChatCompletions(app: FastifyInstance, cfg: Config): void {
  app.post("/v1/chat/completions", async (req: FastifyRequest, reply: FastifyReply) => {
    const body = (req.body ?? {}) as ChatCompletionsBody;
    const model = typeof body.model === "string" && body.model.length > 0 ? body.model : "default";
    const isStream = body.stream === true;

    const capability = `openai:chat-completions:${model}`;
    // Streaming and non-streaming are different offerings under the same
    // capability — different modes (http-stream@v0 vs http-reqresp@v0)
    // and typically different backends.
    const offering = isStream ? "stream" : cfg.defaultOffering;
    const bodyStr = JSON.stringify(body);

    try {
      if (isStream) {
        const result = await httpStream.send({
          brokerUrl: cfg.brokerUrl,
          capability,
          offering,
          paymentBlob: cfg.paymentBlob,
          body: bodyStr,
          contentType: "application/json",
        });
        // Pass through SSE body. Streaming pass-through is buffered in v0.1
        // (the http-stream client reads to EOF for trailers); the body shape
        // is correct, only timing differs. Tracked as tech-debt.
        await reply
          .code(result.status)
          .header("Content-Type", "text/event-stream")
          .send(result.body);
        return;
      }

      const result = await httpReqresp.send({
        brokerUrl: cfg.brokerUrl,
        capability,
        offering,
        paymentBlob: cfg.paymentBlob,
        body: bodyStr,
        contentType: "application/json",
      });
      await reply
        .code(result.status)
        .header("Content-Type", result.headers.get("Content-Type") ?? "application/json")
        .send(Buffer.from(result.body));
    } catch (err) {
      handleBrokerError(reply, err);
    }
  });
}

function handleBrokerError(reply: FastifyReply, err: unknown): void {
  if (err instanceof LivepeerBrokerError) {
    void reply
      .code(err.status >= 500 ? 502 : err.status)
      .header("Content-Type", "application/json")
      .send({ error: err.code, message: err.message });
    return;
  }
  void reply
    .code(500)
    .header("Content-Type", "application/json")
    .send({ error: "internal_error", message: (err as Error).message });
}
