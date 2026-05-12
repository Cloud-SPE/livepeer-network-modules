import type { FastifyInstance, FastifyReply, FastifyRequest } from "fastify";
import type { auth, billing } from "@livepeer-rewrite/customer-portal";
import { middleware } from "@livepeer-rewrite/customer-portal";

import { Capability } from "../livepeer/capabilityMap.js";
import { LivepeerBrokerError } from "../livepeer/errors.js";
import { readOrSynthRequestId } from "../livepeer/requestId.js";
import { HEADER } from "../livepeer/headers.js";
import { resolveDefaultOffering } from "../service/offerings.js";
import { dispatchReqresp } from "../service/routeDispatch.js";
import type { RouteSelector } from "../service/routeSelector.js";
import type { Config } from "../config.js";
import type { Queryable } from "../repo/rateCard.js";
import { createNonChatBillingService } from "../service/nonChatBilling.js";

interface AudioSpeechBody {
  model?: string;
  input?: string;
  voice?: string;
  response_format?: string;
  speed?: number;
  [k: string]: unknown;
}

interface AudioSpeechDispatchBody extends AudioSpeechBody {
  _livepeer_input_chars: number;
}

type AuthResolver = auth.AuthResolver;
type Wallet = billing.Wallet;

export interface RegisterAudioSpeechBillingDeps {
  authResolver: AuthResolver;
  wallet: Wallet;
  rateCardStore: Queryable;
}

export function registerAudioSpeech(
  app: FastifyInstance,
  cfg: Config,
  routeSelector: RouteSelector,
  billingDeps?: RegisterAudioSpeechBillingDeps,
): void {
  const speechBilling = billingDeps
    ? createNonChatBillingService({
        wallet: billingDeps.wallet,
        rateCardStore: billingDeps.rateCardStore,
      })
    : null;
  const preHandler = billingDeps
    ? middleware.authPreHandler(billingDeps.authResolver)
    : undefined;

  app.post("/v1/audio/speech", { ...(preHandler ? { preHandler } : {}) }, async (req: FastifyRequest, reply: FastifyReply) => {
    const requestId = readOrSynthRequestId(req);
    if (!cfg.audioSpeechEnabled) {
      await reply
        .code(503)
        .header("Content-Type", "application/json")
        .header(HEADER.REQUEST_ID, requestId)
        .header(HEADER.ERROR, "mode_unsupported")
        .send({
          error: "mode_unsupported",
          message:
            "/v1/audio/speech is disabled; set OPENAI_AUDIO_SPEECH_ENABLED=true to enable the gateway route",
        });
      return;
    }

    const body = (req.body ?? {}) as AudioSpeechBody;
    const capability = Capability.AudioSpeech;
    const offering =
      (typeof body.model === "string" && body.model.length > 0 ? body.model : null) ??
      resolveDefaultOffering(cfg.offerings, { capability }) ??
      cfg.defaultOffering;

    const reservationHandle =
      speechBilling && req.caller
        ? await speechBilling.reserveSpeech(req.caller, requestId, offering, body)
        : null;
    const dispatchBody: AudioSpeechDispatchBody = {
      ...body,
      _livepeer_input_chars: normalizeSpeechInput(body.input),
    };
    let settled = false;

    try {
      const result = await dispatchReqresp({
        routeSelector,
        request: req,
        capability,
        offering,
        body: JSON.stringify(dispatchBody),
        contentType: "application/json",
        requestId,
      });
      if (speechBilling) {
        await speechBilling.commitSpeech(reservationHandle, offering, body, result.workUnits);
        settled = true;
      }
      await reply
        .code(result.status)
        .header("Content-Type", result.headers.get("Content-Type") ?? "application/octet-stream")
        .header(HEADER.REQUEST_ID, requestId)
        .send(Buffer.from(result.body));
    } catch (err) {
      if (speechBilling && reservationHandle && !settled) {
        try {
          await speechBilling.refund(reservationHandle);
        } catch (refundErr) {
          req.log.error({ err: refundErr, requestId }, "audio speech reservation refund failed");
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
  });
}

function normalizeSpeechInput(input: unknown): number {
  return typeof input === "string" ? input.length : 0;
}
