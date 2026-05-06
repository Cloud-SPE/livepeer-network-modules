import type { FastifyInstance, FastifyReply, FastifyRequest } from "fastify";

import * as httpMultipart from "../livepeer/http-multipart.js";
import { LivepeerBrokerError } from "../livepeer/errors.js";
import { buildPayment } from "../livepeer/payment.js";
import type { Config } from "../config.js";

/**
 * /v1/audio/transcriptions accepts multipart/form-data with `file` + `model`.
 *
 * v0.1: this route forwards the raw multipart body verbatim to the broker
 * with the original Content-Type (preserving the boundary). The model is
 * extracted from a Livepeer-Model header that the caller passes through —
 * a pragmatic shortcut so we don't have to multipart-parse the body just
 * to read the form-field. A real production gateway would parse the
 * multipart stream, extract `model`, and rebuild on the way out.
 */
export function registerAudioTranscriptions(app: FastifyInstance, cfg: Config): void {
  app.post(
    "/v1/audio/transcriptions",
    {
      // Accept any content type; we forward bytes verbatim.
      bodyLimit: 100 * 1024 * 1024, // 100 MiB
    },
    async (req: FastifyRequest, reply: FastifyReply) => {
      const contentType = req.headers["content-type"];
      if (!contentType || !contentType.startsWith("multipart/form-data")) {
        await reply
          .code(400)
          .send({ error: "bad_request", message: "Content-Type must be multipart/form-data" });
        return;
      }

      // Pragmatic v0.1: model from a Livepeer-Model header (caller-provided).
      const model = (req.headers["livepeer-model"] as string | undefined) ?? "default";
      const capability = `openai:audio-transcriptions:${model}`;

      // The multipart content-type parser registered in server.ts gives
      // us the raw body as a Buffer.
      const body = req.body as Buffer | undefined;
      if (!body || !Buffer.isBuffer(body)) {
        await reply.code(400).send({ error: "bad_request", message: "empty multipart body" });
        return;
      }

      try {
        const result = await httpMultipart.send({
          brokerUrl: cfg.brokerUrl,
          capability,
          offering: cfg.defaultOffering,
          paymentBlob: await buildPayment({ capabilityId: capability, offeringId: cfg.defaultOffering }),
          body,
          contentType,
        });
        await reply
          .code(result.status)
          .header("Content-Type", result.headers.get("Content-Type") ?? "application/json")
          .send(Buffer.from(result.body));
      } catch (err) {
        if (err instanceof LivepeerBrokerError) {
          await reply
            .code(err.status >= 500 ? 502 : err.status)
            .send({ error: err.code, message: err.message });
          return;
        }
        await reply.code(500).send({ error: "internal_error", message: (err as Error).message });
      }
    },
  );
}
