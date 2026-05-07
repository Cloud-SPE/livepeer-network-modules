import Fastify from "fastify";
import type { FastifyInstance } from "fastify";

import type { Config } from "./config.js";
import { registerBillingTopupRoutes } from "./routes/billingTopup.js";
import { registerSessionControlWsRoutes } from "./routes/sessionControlWs.js";
import { registerSessionsRoutes } from "./routes/sessions.js";
import { registerStripeWebhookRoutes } from "./routes/stripeWebhook.js";
import { createSessionRelay } from "./service/relay/sessionRelay.js";

export async function buildServer(cfg: Config): Promise<FastifyInstance> {
  const app = Fastify({
    logger: { level: cfg.logLevel },
    bodyLimit: 16 * 1024 * 1024,
  });

  app.get("/healthz", async (_req, reply) => {
    await reply.code(200).header("Content-Type", "text/plain").send("ok\n");
  });

  const relay = createSessionRelay();

  await app.register(registerSessionsRoutes, { cfg });
  await app.register(registerSessionControlWsRoutes, { cfg, relay });
  await app.register(registerBillingTopupRoutes, { cfg });
  await app.register(registerStripeWebhookRoutes, { cfg });

  return app;
}
