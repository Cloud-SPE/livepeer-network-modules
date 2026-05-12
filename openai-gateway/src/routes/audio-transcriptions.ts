import type { FastifyInstance, FastifyReply, FastifyRequest } from "fastify";
import type { auth, billing } from "@livepeer-rewrite/customer-portal";
import { middleware } from "@livepeer-rewrite/customer-portal";

import { Capability } from "../livepeer/capabilityMap.js";
import { LivepeerBrokerError } from "../livepeer/errors.js";
import { readOrSynthRequestId } from "../livepeer/requestId.js";
import { HEADER } from "../livepeer/headers.js";
import { resolveDefaultOffering } from "../service/offerings.js";
import { extractMultipartField } from "../service/multipart.js";
import { dispatchMultipart } from "../service/routeDispatch.js";
import type { RouteSelector } from "../service/routeSelector.js";
import type { Config } from "../config.js";
import type { Queryable } from "../repo/rateCard.js";
import { createNonChatBillingService } from "../service/nonChatBilling.js";

type AuthResolver = auth.AuthResolver;
type Wallet = billing.Wallet;

export interface RegisterAudioTranscriptionsBillingDeps {
  authResolver: AuthResolver;
  wallet: Wallet;
  rateCardStore: Queryable;
}

export function registerAudioTranscriptions(
  app: FastifyInstance,
  cfg: Config,
  routeSelector: RouteSelector,
  billingDeps?: RegisterAudioTranscriptionsBillingDeps,
): void {
  const transcriptionsBilling = billingDeps
    ? createNonChatBillingService({
        wallet: billingDeps.wallet,
        rateCardStore: billingDeps.rateCardStore,
      })
    : null;
  const preHandler = billingDeps
    ? middleware.authPreHandler(billingDeps.authResolver)
    : undefined;
  app.post(
    "/v1/audio/transcriptions",
    {
      bodyLimit: 100 * 1024 * 1024,
      ...(preHandler ? { preHandler } : {}),
    },
    async (req: FastifyRequest, reply: FastifyReply) => {
      const contentType = req.headers["content-type"];
      const requestId = readOrSynthRequestId(req);
      if (!contentType || !contentType.startsWith("multipart/form-data")) {
        await reply
          .code(400)
          .header(HEADER.REQUEST_ID, requestId)
          .send({ error: "bad_request", message: "Content-Type must be multipart/form-data" });
        return;
      }

      const capability = Capability.AudioTranscriptions;
      const body = req.body as Buffer | undefined;
      if (!body || !Buffer.isBuffer(body)) {
        await reply
          .code(400)
          .header(HEADER.REQUEST_ID, requestId)
          .send({ error: "bad_request", message: "empty multipart body" });
        return;
      }
      const modelField = extractMultipartField(body, contentType, "model");
      const modelHeader = req.headers["livepeer-model"] as string | undefined;
      const offering =
        modelField ??
        (modelHeader && modelHeader.length > 0 ? modelHeader : null) ??
        resolveDefaultOffering(cfg.offerings, { capability }) ??
        cfg.defaultOffering;
      const reservationHandle =
        transcriptionsBilling && req.caller
          ? await transcriptionsBilling.reserveTranscription(req.caller, requestId, offering, body.byteLength)
          : null;
      let settled = false;

      try {
        const result = await dispatchMultipart({
          routeSelector,
          request: req,
          capability,
          offering,
          body,
          contentType,
          requestId,
        });
        if (transcriptionsBilling) {
          await transcriptionsBilling.commitTranscription(
            reservationHandle,
            offering,
            body.byteLength,
            result.workUnits,
          );
          settled = true;
        }
        await reply
          .code(result.status)
          .header("Content-Type", result.headers.get("Content-Type") ?? "application/json")
          .header(HEADER.REQUEST_ID, requestId)
          .send(Buffer.from(result.body));
      } catch (err) {
        if (transcriptionsBilling && reservationHandle && !settled) {
          try {
            await transcriptionsBilling.refund(reservationHandle);
          } catch (refundErr) {
            req.log.error({ err: refundErr, requestId }, "transcription reservation refund failed");
          }
        }
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
    },
  );
}
