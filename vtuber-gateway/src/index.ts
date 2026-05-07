import { loadConfig } from "./config.js";
import { buildServer } from "./server.js";
import type { VtuberGatewayDeps } from "./runtime/deps.js";
import { createInMemorySessionStore } from "./service/sessions/inMemorySessionStore.js";

async function main(): Promise<void> {
  const cfg = loadConfig();

  const sessionStore = createInMemorySessionStore();

  const deps: VtuberGatewayDeps = {
    cfg,
    sessionStore,
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
