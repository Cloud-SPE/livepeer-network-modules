import type { FastifyInstance, FastifyPluginAsync } from "fastify";
import { z } from "zod";

import type { VtuberGatewayDeps } from "../runtime/deps.js";
import { authPreHandler } from "@livepeer-rewrite/customer-portal/middleware";

export const BillingTopupRequestSchema = z.object({
  cents: z.number().int().positive(),
  success_url: z.string().url(),
  cancel_url: z.string().url(),
});

export interface BillingTopupRouteDeps {
  deps: VtuberGatewayDeps;
}

const DEFAULT_TOPUP_MIN_CENTS = 500;
const DEFAULT_TOPUP_MAX_CENTS = 100_000_00;

export const registerBillingTopupRoutes: FastifyPluginAsync<
  BillingTopupRouteDeps
> = async (app: FastifyInstance, { deps }: BillingTopupRouteDeps) => {
  app.post(
    "/v1/billing/topup",
    { preHandler: authPreHandler(deps.authResolver) },
    async (req, reply) => {
      const parsed = BillingTopupRequestSchema.safeParse(req.body);
      if (!parsed.success) {
        await reply
          .code(400)
          .send({ error: "invalid_request", details: parsed.error.issues });
        return;
      }
      if (deps.stripe === undefined) {
        await reply.code(503).send({ error: "stripe_not_configured" });
        return;
      }
      const customerId = req.caller?.id;
      if (typeof customerId !== "string") {
        await reply.code(401).send({ error: "authentication_required" });
        return;
      }
      const cfg = deps.topupConfig ?? {
        priceMinCents: DEFAULT_TOPUP_MIN_CENTS,
        priceMaxCents: DEFAULT_TOPUP_MAX_CENTS,
      };
      if (
        parsed.data.cents < cfg.priceMinCents ||
        parsed.data.cents > cfg.priceMaxCents
      ) {
        await reply.code(400).send({
          error: "amount_out_of_range",
          min_cents: cfg.priceMinCents,
          max_cents: cfg.priceMaxCents,
        });
        return;
      }
      try {
        const result = await deps.stripe.createCheckoutSession({
          customerId,
          amountUsdCents: parsed.data.cents,
          successUrl: parsed.data.success_url,
          cancelUrl: parsed.data.cancel_url,
        });
        await reply.code(200).send({
          stripe_session_id: result.sessionId,
          stripe_checkout_url: result.url,
        });
      } catch (err) {
        req.log.error({ err }, "stripe createCheckoutSession failed");
        await reply.code(502).send({ error: "stripe_unavailable" });
      }
    },
  );
};
