import { existsSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import Fastify, { type FastifyInstance } from "fastify";
import { drizzle } from "drizzle-orm/node-postgres";
import {
  auth,
  createCustomerPortal,
  db as portalDb,
  registerCustomerSelfServiceRoutes,
} from "@livepeer-network-modules/customer-portal";
import type { Config } from "./config.js";
import { createGatewayClient } from "./service/gatewayClient.js";
import { registerWaitlistRoutes } from "./routes/waitlist.js";
import { registerLoginRoutes } from "./routes/login.js";
import { registerSessionRoutes } from "./routes/sessions.js";
import { registerPromptRoutes } from "./routes/prompts.js";
import { registerUsageRoutes } from "./routes/usage.js";
import { registerDaydreamAdminRoutes } from "./routes/admin.js";

const HERE = dirname(fileURLToPath(import.meta.url));
const PACKAGE_ROOT = resolve(HERE, "..");
const REPO_ROOT = resolve(PACKAGE_ROOT, "..");

function resolveCustomerPortalMigrationsDir(): string {
  // When the runtime image bundles them as a sibling directory
  // (mirrors the vtuber-gateway pattern) — preferred in production.
  const packaged = resolve(PACKAGE_ROOT, "customer-portal-migrations");
  if (existsSync(packaged)) return packaged;
  // Local repo layout (`pnpm dev` from monorepo root).
  const local = resolve(REPO_ROOT, "customer-portal", "migrations");
  if (existsSync(local)) return local;
  throw new Error(
    `could not locate customer-portal migrations; checked ${packaged} and ${local}`,
  );
}

function resolveDaydreamMigrationsDir(): string {
  return resolve(PACKAGE_ROOT, "migrations");
}

export interface BuiltServer {
  app: FastifyInstance;
  close(): Promise<void>;
}

export async function buildServer(config: Config): Promise<BuiltServer> {
  const app = Fastify({
    logger: { level: process.env.LOG_LEVEL ?? "info" },
    trustProxy: true,
  });

  // Two pools against the same Postgres: one typed against
  // customer-portal's schema (for createCustomerPortal), one untyped
  // for daydream-local tables. Sharing the underlying database lets
  // both migration sets coexist without a second instance.
  const portalPool = portalDb.createPool({
    connectionString: config.postgresUrl,
    max: 10,
  });
  const portalDbConn = portalDb.makeDb(portalPool);
  await portalDb.runMigrations(portalDbConn, resolveCustomerPortalMigrationsDir());

  const daydreamPool = portalDb.createPool({
    connectionString: config.postgresUrl,
    max: 5,
  });
  const daydreamDbConn = drizzle(daydreamPool);
  await portalDb.runMigrations(daydreamDbConn as never, resolveDaydreamMigrationsDir());

  const portal = createCustomerPortal({
    db: portalDbConn,
    pepper: config.authPepper,
    cacheTtlMs: 30_000,
    admin:
      config.adminTokens.length > 0 ? { tokens: config.adminTokens } : undefined,
    // Stripe intentionally omitted.
    envPrefix: "live",
  });

  // Fall back to a no-op admin resolver so the routes stay mounted in
  // dev when DAYDREAM_PORTAL_ADMIN_TOKENS is empty (all admin calls 401).
  const adminResolver: auth.AdminAuthResolver =
    portal.adminAuthResolver ??
    auth.createStaticAdminTokenAuthResolver({ tokens: [] });

  const gateway = createGatewayClient({ baseUrl: config.gatewayBaseUrl });

  // customer-portal's /portal/signup creates a customer immediately,
  // bypassing our waitlist gate. We mount its self-service bundle for
  // login + key issue + auth-token paths but the daydream SPA never
  // calls /portal/signup directly.
  registerCustomerSelfServiceRoutes(app, {
    db: portalDbConn,
    portal,
    authPepper: config.authPepper,
  });

  registerWaitlistRoutes(app, { db: daydreamDbConn });
  registerLoginRoutes(app, { portal });
  registerSessionRoutes(app, {
    db: daydreamDbConn,
    portal,
    gateway,
    capability: config.capability,
  });
  registerPromptRoutes(app, { db: daydreamDbConn, portal });
  registerUsageRoutes(app, { db: daydreamDbConn, portal });
  registerDaydreamAdminRoutes(app, {
    db: daydreamDbConn,
    portal,
    adminAuthResolver: adminResolver,
  });

  app.get("/healthz", async () => ({ ok: true }));

  return {
    app,
    close: async () => {
      await app.close();
      await Promise.all([portalPool.end(), daydreamPool.end()]);
    },
  };
}
