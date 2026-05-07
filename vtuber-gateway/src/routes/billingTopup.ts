import type { FastifyInstance, FastifyPluginAsync } from "fastify";
import { z } from "zod";

import type { VtuberGatewayDeps } from "../runtime/deps.js";

export const BillingTopupRequestSchema = z.object({
  cents: z.number().int().positive(),
  success_url: z.string().url(),
  cancel_url: z.string().url(),
});

export interface BillingTopupRouteDeps {
  deps: VtuberGatewayDeps;
}

export const registerBillingTopupRoutes: FastifyPluginAsync<
  BillingTopupRouteDeps
> = async (app: FastifyInstance, { deps }: BillingTopupRouteDeps) => {
  app.post("/v1/billing/topup", async (req, reply) => {
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
    void req;
    await reply.code(503).send({ error: "billing_topup_unimplemented" });
  });
};
