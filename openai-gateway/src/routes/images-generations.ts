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
import { createChainedAuthResolver } from "../service/auth.js";
import { createNonChatBillingService } from "../service/nonChatBilling.js";

interface ImagesGenerationsBody {
  model?: string;
  prompt?: string;
  size?: string;
  quality?: string;
  n?: number;
  [k: string]: unknown;
}

type AuthResolver = auth.AuthResolver;
type Wallet = billing.Wallet;

export interface RegisterImagesBillingDeps {
  authResolver: AuthResolver;
  uiAuthResolver?: AuthResolver;
  wallet: Wallet;
  rateCardStore: Queryable;
}

export function registerImagesGenerations(
  app: FastifyInstance,
  cfg: Config,
  routeSelector: RouteSelector,
  billingDeps?: RegisterImagesBillingDeps,
): void {
  const imagesBilling = billingDeps
    ? createNonChatBillingService({
        wallet: billingDeps.wallet,
        rateCardStore: billingDeps.rateCardStore,
      })
    : null;
  const preHandler = billingDeps
    ? middleware.authPreHandler(
        createChainedAuthResolver(billingDeps.authResolver, billingDeps.uiAuthResolver),
      )
    : undefined;

  app.post("/v1/images/generations", { ...(preHandler ? { preHandler } : {}) }, async (req: FastifyRequest, reply: FastifyReply) => {
    const body = (req.body ?? {}) as ImagesGenerationsBody;
    const capability = Capability.ImagesGenerations;
    const offering =
      (typeof body.model === "string" && body.model.length > 0 ? body.model : null) ??
      resolveDefaultOffering(cfg.offerings, { capability }) ??
      cfg.defaultOffering;
    const requestId = readOrSynthRequestId(req);
    const reservationHandle =
      imagesBilling && req.caller
        ? await imagesBilling.reserveImages(req.caller, requestId, offering, body)
        : null;
    let settled = false;

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
      if (imagesBilling) {
        await imagesBilling.commitImages(reservationHandle, offering, body, result.body);
        settled = true;
      }
      await reply
        .code(result.status)
        .header("Content-Type", result.headers.get("Content-Type") ?? "application/json")
        .header(HEADER.REQUEST_ID, requestId)
        .send(Buffer.from(result.body));
    } catch (err) {
      if (imagesBilling && reservationHandle && !settled) {
        try {
          await imagesBilling.refund(reservationHandle);
        } catch (refundErr) {
          req.log.error({ err: refundErr, requestId }, "images reservation refund failed");
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
