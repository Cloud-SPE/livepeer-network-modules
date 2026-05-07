// POST /v1/billing/topup — Stripe checkout session for a customer
// balance top-up. Delegates to `customer-portal/`'s shared shell.
//
// Source: `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/
// http/billing/topup.ts`.

import type { FastifyInstance, FastifyPluginAsync } from "fastify";
import { z } from "zod";

import type { Config } from "../config.js";

export const BillingTopupRequestSchema = z.object({
  cents: z.number().int().positive(),
  success_url: z.string().url(),
  cancel_url: z.string().url(),
});

export interface BillingTopupRouteDeps {
  cfg: Config;
}

export const registerBillingTopupRoutes: FastifyPluginAsync<
  BillingTopupRouteDeps
> = async (app: FastifyInstance, deps: BillingTopupRouteDeps) => {
  void deps;
  app.post("/v1/billing/topup", async (req, reply) => {
    const parsed = BillingTopupRequestSchema.safeParse(req.body);
    if (!parsed.success) {
      await reply
        .code(400)
        .send({ error: "invalid_request", details: parsed.error.issues });
      return;
    }
    await reply.code(503).send({ error: "billing_topup_unimplemented" });
  });
};
