import Fastify from "fastify";
import type { FastifyInstance } from "fastify";
import { admin as portalAdmin } from "@livepeer-rewrite/customer-portal";
import type { CustomerPortal } from "@livepeer-rewrite/customer-portal";
import type { Db as PortalDb } from "@livepeer-rewrite/customer-portal/db";

import type { Db as VtuberDb } from "./db/pool.js";
import { registerAdmin } from "./routes/admin.js";
import type { VtuberGatewayDeps } from "./runtime/deps.js";
import { registerBillingTopupRoutes } from "./routes/billingTopup.js";
import { registerSessionControlWsRoutes } from "./routes/sessionControlWs.js";
import { registerSessionsRoutes } from "./routes/sessions.js";
import { registerStripeWebhookRoutes } from "./routes/stripeWebhook.js";
import { createSessionRelay } from "./service/relay/sessionRelay.js";

export async function buildServer(
  deps: VtuberGatewayDeps,
  extras?: {
    portalDb?: PortalDb;
    portal?: CustomerPortal;
    vtuberDb?: VtuberDb;
  },
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
  await app.register(registerSessionControlWsRoutes, {
    cfg: deps.cfg,
    relay,
    ...(deps.reconnectWindow !== undefined
      ? { reconnectWindow: deps.reconnectWindow }
      : {}),
  });
  await app.register(registerBillingTopupRoutes, { deps });
  await app.register(registerStripeWebhookRoutes, { deps });
  if (extras?.portalDb && extras.portal?.adminAuthResolver) {
    portalAdmin.registerAdminRoutes(app, {
      db: extras.portalDb,
      engine: extras.portal.adminEngine,
      authResolver: extras.portal.adminAuthResolver,
      customerTokenService: extras.portal.customerTokenService,
      issueApiKey: extras.portal.issueApiKey,
      revokeApiKey: extras.portal.revokeApiKey,
    });
  }
  if (extras?.vtuberDb && extras.portal?.adminAuthResolver) {
    registerAdmin(app, {
      authResolver: extras.portal.adminAuthResolver,
      db: extras.vtuberDb,
      sessionStore: deps.sessionStore,
      serviceRegistry: deps.serviceRegistry,
      vtuberRateCardUsdPerSecond: deps.cfg.vtuberRateCardUsdPerSecond,
    });
  }

  return app;
}
