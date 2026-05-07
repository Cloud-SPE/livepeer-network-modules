import Fastify from "fastify";
import type { FastifyInstance } from "fastify";

import type { VtuberGatewayDeps } from "./runtime/deps.js";
import { registerBillingTopupRoutes } from "./routes/billingTopup.js";
import { registerSessionControlWsRoutes } from "./routes/sessionControlWs.js";
import { registerSessionsRoutes } from "./routes/sessions.js";
import { registerStripeWebhookRoutes } from "./routes/stripeWebhook.js";
import { createSessionRelay } from "./service/relay/sessionRelay.js";

export async function buildServer(
  deps: VtuberGatewayDeps,
): Promise<FastifyInstance> {
  const app = Fastify({
    logger: { level: deps.cfg.logLevel },
    bodyLimit: 16 * 1024 * 1024,
  });

  app.get("/healthz", async (_req, reply) => {
    await reply.code(200).header("Content-Type", "text/plain").send("ok\n");
  });

  const relay = createSessionRelay();

  await app.register(registerSessionsRoutes, { deps });
  await app.register(registerSessionControlWsRoutes, { cfg: deps.cfg, relay });
  await app.register(registerBillingTopupRoutes, { deps });
  await app.register(registerStripeWebhookRoutes, { deps });

  return app;
}
