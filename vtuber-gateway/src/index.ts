import { existsSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { createCustomerPortal, db as portalDb } from "@livepeer-rewrite/customer-portal";
import { registerCustomerSelfServiceRoutes } from "@livepeer-rewrite/customer-portal/routes";

import { loadConfig } from "./config.js";
import * as vtuberDb from "./db/pool.js";
import { buildServer } from "./server.js";
import { createGrpcPayerDaemonClient } from "./providers/payerDaemon.js";
import { createServiceRegistryClient } from "./providers/serviceRegistry.js";
import { createBrokerWorkerClient } from "./providers/workerClient.js";
import { vtuberRateCardSession } from "./repo/schema.js";
import type { VtuberGatewayDeps } from "./runtime/deps.js";
import { createReconnectWindow } from "./service/relay/reconnectWindow.js";
import { createPostgresSessionStore } from "./service/sessions/postgresSessionStore.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const VTUBER_GATEWAY_ROOT = resolve(__dirname, "..");
const REPO_ROOT = resolve(VTUBER_GATEWAY_ROOT, "..");

function resolveCustomerPortalMigrationsDir(): string {
  const packagedPath = resolve(VTUBER_GATEWAY_ROOT, "customer-portal-migrations");
  if (existsSync(packagedPath)) return packagedPath;

  const localRepoPath = resolve(REPO_ROOT, "customer-portal", "migrations");
  if (existsSync(localRepoPath)) return localRepoPath;

  throw new Error(
    `could not locate customer-portal migrations; checked ${packagedPath} and ${localRepoPath}`,
  );
}

async function main(): Promise<void> {
  const cfg = loadConfig();
  const portalPool = portalDb.createPool({ connectionString: cfg.databaseUrl, max: 10 });
  const portalDbConn = portalDb.makeDb(portalPool);
  await portalDb.runMigrations(portalDbConn, resolveCustomerPortalMigrationsDir());
  const portal = createCustomerPortal({
    db: portalDbConn,
    pepper: cfg.customerPortalPepper,
    admin: cfg.adminTokens.length > 0 ? { tokens: cfg.adminTokens } : undefined,
  });
  const vtuberPool = vtuberDb.createPool({ connectionString: cfg.databaseUrl, max: 10 });
  const vtuberDbConn = vtuberDb.makeDb(vtuberPool);
  await vtuberDbConn
    .insert(vtuberRateCardSession)
    .values({
      offering: "default",
      usdPerSecond: cfg.vtuberRateCardUsdPerSecond,
    })
    .onConflictDoNothing({ target: vtuberRateCardSession.offering });

  const sessionStore = createPostgresSessionStore(vtuberDbConn);
  const reconnectWindow = createReconnectWindow({
    cfg: {
      windowMs: cfg.vtuberControlReconnectWindowMs,
      bufferMessages: cfg.vtuberControlReconnectBufferMessages,
      bufferBytes: cfg.vtuberControlReconnectBufferBytes,
    },
    onWindowExpiry: (sessionId) => {
      // Best-effort teardown: marks the session ended in the store. Full
      // worker.stopSession() + payerDaemon session-finalize is deferred
      // until a SessionTeardown service lands in a future followup
      // (gated on payerDaemon exposing a session-finalize gRPC).
      void sessionStore
        .updateSession(sessionId, { status: "ended", endedAt: new Date() })
        .catch(() => {});
    },
  });
  const payerDaemon = await createGrpcPayerDaemonClient({
    socketPath: cfg.payerDaemonSocket,
    protoRoot: cfg.paymentProtoRoot,
  });
  const serviceRegistry = createServiceRegistryClient({
    brokerUrl: cfg.brokerUrl,
    resolverSocket: cfg.resolverSocket,
    resolverProtoRoot: cfg.resolverProtoRoot,
    resolverSnapshotTtlMs: cfg.resolverSnapshotTtlMs,
  });
  const worker = createBrokerWorkerClient();

  const deps: VtuberGatewayDeps = {
    cfg,
    sessionStore,
    reconnectWindow,
    authResolver: portal.authResolver,
    payerDaemon,
    serviceRegistry,
    worker,
    vtuberDb: vtuberDbConn,
  };

  const app = await buildServer(deps, {
    portalDb: portalDbConn,
    portal,
    vtuberDb: vtuberDbConn,
  });
  registerCustomerSelfServiceRoutes(app, {
    db: portalDbConn,
    portal,
    authPepper: cfg.customerPortalPepper,
  });
  try {
    await app.listen({ host: "0.0.0.0", port: cfg.listenPort });
  } catch (err) {
    app.log.error(err, "failed to listen");
    await deps.payerDaemon.close().catch(() => {});
    await deps.serviceRegistry.close().catch(() => {});
    await vtuberPool.end().catch(() => {});
    await portalPool.end().catch(() => {});
    process.exit(1);
  }
}

void main();
