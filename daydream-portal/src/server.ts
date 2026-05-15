import Fastify, { type FastifyInstance } from "fastify";
import pg from "pg";
import { drizzle } from "drizzle-orm/node-postgres";
import {
  auth,
  createCustomerPortal,
  registerCustomerSelfServiceRoutes,
} from "@livepeer-network-modules/customer-portal";
import type { Config } from "./config.js";
import { createGatewayClient } from "./service/gatewayClient.js";
import { registerWaitlistRoutes } from "./routes/waitlist.js";
import { registerSessionRoutes } from "./routes/sessions.js";
import { registerPromptRoutes } from "./routes/prompts.js";
import { registerUsageRoutes } from "./routes/usage.js";
import { registerDaydreamAdminRoutes } from "./routes/admin.js";

export interface BuiltServer {
  app: FastifyInstance;
  close(): Promise<void>;
}

export async function buildServer(config: Config): Promise<BuiltServer> {
  const app = Fastify({
    logger: { level: process.env.LOG_LEVEL ?? "info" },
    trustProxy: true,
  });

  const pool = new pg.Pool({ connectionString: config.postgresUrl, max: 10 });
  const db = drizzle(pool);

  const portal = createCustomerPortal({
    db: db as never,
    pepper: config.authPepper,
    cacheTtlMs: 30_000,
    admin:
      config.adminTokens.length > 0 ? { tokens: config.adminTokens } : undefined,
    // Stripe intentionally omitted — daydream-portal does not run
    // payments. Wallet/topup paths from customer-portal exist on the
    // type surface but are never wired into routes here.
    envPrefix: "live",
  });

  // When no admin tokens are configured, fall back to a resolver that
  // always 401s — admin routes stay mounted in dev but unreachable
  // without explicit operator setup.
  const adminResolver: auth.AdminAuthResolver =
    portal.adminAuthResolver ??
    auth.createStaticAdminTokenAuthResolver({ tokens: [] });

  const gateway = createGatewayClient({ baseUrl: config.gatewayBaseUrl });

  // customer-portal ships /portal/signup which would create a customer
  // directly. We want the waitlist gate first, so we DO NOT register
  // customerSelfService here — instead we register the subset that's
  // safe (login + key list + key issue) by composing manually.
  //
  // Until customer-portal exposes a finer-grained registration helper,
  // we register the full bundle and rely on the waitlist+admin flow to
  // be the production path; the customer-portal /portal/signup
  // endpoint stays accessible at the HTTP layer but the portal SPA
  // never calls it.
  registerCustomerSelfServiceRoutes(app, {
    db: db as never,
    portal,
    authPepper: config.authPepper,
  });

  registerWaitlistRoutes(app, { db });
  registerSessionRoutes(app, {
    db,
    portal,
    gateway,
    capability: config.capability,
  });
  registerPromptRoutes(app, { db, portal });
  registerUsageRoutes(app, { db, portal });
  registerDaydreamAdminRoutes(app, {
    db,
    portal,
    adminAuthResolver: adminResolver,
  });

  app.get("/healthz", async () => ({ ok: true }));

  return {
    app,
    close: async () => {
      await app.close();
      await pool.end();
    },
  };
}
