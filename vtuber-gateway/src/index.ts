import { loadConfig } from "./config.js";
import { buildServer } from "./server.js";
import type { VtuberGatewayDeps } from "./runtime/deps.js";
import { createReconnectWindow } from "./service/relay/reconnectWindow.js";
import { createInMemorySessionStore } from "./service/sessions/inMemorySessionStore.js";

async function main(): Promise<void> {
  const cfg = loadConfig();

  const sessionStore = createInMemorySessionStore();
  const reconnectWindow = createReconnectWindow({
    cfg: {
      windowMs: cfg.vtuberControlReconnectWindowMs,
      bufferMessages: cfg.vtuberControlReconnectBufferMessages,
      bufferBytes: cfg.vtuberControlReconnectBufferBytes,
    },
    onWindowExpiry: (sessionId) => {
      void sessionStore
        .updateSession(sessionId, { status: "ended", endedAt: new Date() })
        .catch(() => {
          // best-effort teardown
        });
    },
  });

  const deps: VtuberGatewayDeps = {
    cfg,
    sessionStore,
    reconnectWindow,
    authResolver: {
      async resolve() {
        return null;
      },
    },
    payerDaemon: {
      async createPayment() {
        throw new Error("payerDaemon client not configured");
      },
      async close() {},
    },
    serviceRegistry: {
      async listVtuberNodes() {
        return [];
      },
      async getNode() {
        return null;
      },
      async select() {
        return null;
      },
      async close() {},
    },
    worker: {
      async startSession() {
        throw new Error("worker client not configured");
      },
      async stopSession() {},
      async topupSession() {},
    },
  };

  const app = await buildServer(deps);
  try {
    await app.listen({ host: "0.0.0.0", port: cfg.listenPort });
  } catch (err) {
    app.log.error(err, "failed to listen");
    process.exit(1);
  }
}

void main();
