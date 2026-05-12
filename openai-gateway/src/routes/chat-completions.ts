import type { FastifyInstance, FastifyReply, FastifyRequest } from "fastify";
import type { auth, billing } from "@livepeer-rewrite/customer-portal";
import { middleware } from "@livepeer-rewrite/customer-portal";

import { Capability } from "../livepeer/capabilityMap.js";
import { LivepeerBrokerError } from "../livepeer/errors.js";
import { readOrSynthRequestId } from "../livepeer/requestId.js";
import { resolveDefaultOffering } from "../service/offerings.js";
import { dispatchReqresp, dispatchStream } from "../service/routeDispatch.js";
import type { RouteSelector } from "../service/routeSelector.js";
import { HEADER } from "../livepeer/headers.js";
import type { Config } from "../config.js";
import { MODE as STREAM_MODE } from "../livepeer/http-stream.js";
import { MODE as REQRESP_MODE } from "../livepeer/http-reqresp.js";
import type { Queryable } from "../repo/rateCard.js";
import {
  createChatBillingService,
  type ChatBillingBody,
} from "../service/chatBilling.js";

interface ChatCompletionsBody extends ChatBillingBody {
  stream?: boolean;
  [k: string]: unknown;
}

type AuthResolver = auth.AuthResolver;
type Wallet = billing.Wallet;

export interface RegisterChatCompletionsBillingDeps {
  authResolver: AuthResolver;
  wallet: Wallet;
  rateCardStore: Queryable;
}

export function registerChatCompletions(
  app: FastifyInstance,
  cfg: Config,
  routeSelector: RouteSelector,
  billingDeps?: RegisterChatCompletionsBillingDeps,
): void {
  const chatBilling = billingDeps
    ? createChatBillingService({
        wallet: billingDeps.wallet,
        rateCardStore: billingDeps.rateCardStore,
      })
    : null;
  const preHandler = billingDeps
    ? middleware.authPreHandler(billingDeps.authResolver)
    : undefined;

  app.post("/v1/chat/completions", { ...(preHandler ? { preHandler } : {}) }, async (req: FastifyRequest, reply: FastifyReply) => {
    const body = (req.body ?? {}) as ChatCompletionsBody;
    const isStream = body.stream === true;
    const interactionMode = isStream ? STREAM_MODE : REQRESP_MODE;
    const capability = Capability.ChatCompletions;
    const variant = isStream ? "streaming" : "non-streaming";

    const offering =
      (typeof body.model === "string" && body.model.length > 0 ? body.model : null) ??
      resolveDefaultOffering(cfg.offerings, { capability, variant }) ??
      cfg.defaultOffering;

    const requestId = readOrSynthRequestId(req);
    const dispatchBody = isStream
      ? withForcedUsageChunk(body)
      : body;
    const bodyStr = JSON.stringify(dispatchBody);
    const reservationHandle =
      chatBilling && req.caller
        ? await chatBilling.reserve(req.caller, requestId, offering, body)
        : null;
    let settled = false;

    try {
      if (isStream) {
        const handle = await dispatchStream({
          routeSelector,
          request: req,
          capability,
          offering,
          interactionMode,
          body: bodyStr,
          contentType: "application/json",
          requestId,
        });
        reply.raw.statusCode = handle.status;
        reply.raw.setHeader("Content-Type", "text/event-stream");
        reply.raw.setHeader("Cache-Control", "no-cache");
        reply.raw.setHeader("Connection", "keep-alive");
        reply.raw.setHeader(HEADER.REQUEST_ID, requestId);
        reply.hijack();
        const transcriptChunks: Buffer[] = [];
        let streamError: unknown = null;
        try {
          for await (const chunk of handle.stream) {
            const buf = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
            transcriptChunks.push(buf);
            reply.raw.write(buf);
          }
        } catch (err) {
          streamError = err;
        } finally {
          reply.raw.end();
        }
        try {
          await handle.done();
        } catch (err) {
          streamError ??= err;
        } finally {
          if (chatBilling && req.caller) {
            try {
              await chatBilling.settleStream(
                req.caller,
                reservationHandle,
                offering,
                body,
                Buffer.concat(transcriptChunks).toString("utf8"),
              );
              settled = true;
            } catch (err) {
              req.log.error({ err, requestId }, "chat stream settlement failed");
            }
          }
        }
        if (streamError) {
          req.log.warn({ err: streamError, requestId }, "chat stream forwarding ended with error");
        }
        return;
      }

      const result = await dispatchReqresp({
        routeSelector,
        request: req,
        capability,
        offering,
        interactionMode,
        body: bodyStr,
        contentType: "application/json",
        requestId,
      });
      if (chatBilling && req.caller) {
        await chatBilling.commitFromResponseBody(req.caller, reservationHandle, offering, result.body);
        settled = true;
      }
      await reply
        .code(result.status)
        .header("Content-Type", result.headers.get("Content-Type") ?? "application/json")
        .header(HEADER.REQUEST_ID, requestId)
        .send(Buffer.from(result.body));
    } catch (err) {
      if (chatBilling && reservationHandle && !settled) {
        try {
          await chatBilling.refund(reservationHandle);
        } catch (refundErr) {
          req.log.error({ err: refundErr, requestId }, "chat reservation refund failed");
        }
      }
      handleBrokerError(reply, err, requestId);
    }
  });
}

function withForcedUsageChunk(body: ChatCompletionsBody): ChatCompletionsBody {
  const streamOptions = body.stream_options ?? {};
  return {
    ...body,
    stream: true,
    stream_options: {
      ...streamOptions,
      include_usage: true,
    },
  };
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
