import type { FastifyInstance, FastifyPluginAsync } from "fastify";

import type { VtuberGatewayDeps } from "../runtime/deps.js";

export interface StripeWebhookRouteDeps {
  deps: VtuberGatewayDeps;
}

export const registerStripeWebhookRoutes: FastifyPluginAsync<
  StripeWebhookRouteDeps
> = async (app: FastifyInstance, { deps }: StripeWebhookRouteDeps) => {
  void deps;
  app.post("/v1/stripe/webhook", async (req, reply) => {
    const sig = req.headers["stripe-signature"];
    if (typeof sig !== "string" || sig.length === 0) {
      await reply.code(400).send({ error: "missing_stripe_signature" });
      return;
    }
    await reply.code(503).send({ error: "stripe_webhook_unimplemented" });
  });
};
