import { createCustomerPortal } from "@livepeer-rewrite/customer-portal";
import { drizzle } from "drizzle-orm/node-postgres";
import pg from "pg";

import { loadConfig } from "./config.js";
import { buildServer } from "./server.js";
import { createGrpcPayerDaemonClient } from "./providers/payerDaemon.js";
import { createServiceRegistryClient } from "./providers/serviceRegistry.js";
import { createBrokerWorkerClient } from "./providers/workerClient.js";
import type { VtuberGatewayDeps } from "./runtime/deps.js";
import { createReconnectWindow } from "./service/relay/reconnectWindow.js";
import { createInMemorySessionStore } from "./service/sessions/inMemorySessionStore.js";

async function main(): Promise<void> {
  const cfg = loadConfig();
  const portalPool = new pg.Pool({ connectionString: cfg.databaseUrl, max: 10 });
  const portal = createCustomerPortal({
    db: drizzle(portalPool),
    pepper: cfg.customerPortalPepper,
  });

  const sessionStore = createInMemorySessionStore();
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
  };

  const app = await buildServer(deps);
  try {
    await app.listen({ host: "0.0.0.0", port: cfg.listenPort });
  } catch (err) {
    app.log.error(err, "failed to listen");
    await deps.payerDaemon.close().catch(() => {});
    await deps.serviceRegistry.close().catch(() => {});
    await portalPool.end().catch(() => {});
    process.exit(1);
  }
}

void main();
