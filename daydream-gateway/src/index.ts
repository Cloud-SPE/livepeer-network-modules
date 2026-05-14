/**
 * daydream-gateway server entrypoint.
 *
 * Boot order:
 *   1. Load config (env)
 *   2. Init payer-daemon client (health-probe)
 *   3. Build orch selector (registry client)
 *   4. Build session router (in-memory map)
 *   5. Register routes + start Fastify listener
 *
 * No DB. No migrations. No customer-portal. See AGENTS.md.
 */

import Fastify from "fastify";

import { loadConfig } from "./config.js";
import { createOrchSelector } from "./orchSelector.js";
import { init as initPayer, shutdown as shutdownPayer } from "./paymentClient.js";
import { SessionRouter } from "./sessionRouter.js";
import { registerOrchRoutes } from "./routes/orchs.js";
import { registerPassthroughRoutes } from "./routes/passthrough.js";
import { registerSessionRoutes } from "./routes/sessions.js";
import { registerStudioStatic } from "./runtime/static.js";

async function main(): Promise<void> {
  const cfg = loadConfig();

  const app = Fastify({
    logger: {
      level: process.env.DAYDREAM_GATEWAY_LOG_LEVEL ?? "info",
    },
  });

  app.get("/healthz", async () => ({ status: "ok" }));

  await initPayer({
    socketPath: cfg.payerDaemonSocket,
    protoRoot: cfg.paymentProtoRoot,
  });
  app.log.info({ socket: cfg.payerDaemonSocket }, "payer-daemon connected");

  const selector = createOrchSelector(cfg);
  const router = new SessionRouter();

  registerOrchRoutes(app, selector);
  registerSessionRoutes(app, cfg, selector, router);
  registerPassthroughRoutes(app, router);
  await registerStudioStatic(app);

  const [host, port] = parseListen(cfg.listen);
  await app.listen({ host, port });
  app.log.info({ listen: cfg.listen }, "daydream-gateway listening");

  const shutdown = async (sig: NodeJS.Signals): Promise<void> => {
    app.log.info({ signal: sig }, "shutting down");
    await app.close();
    shutdownPayer();
    process.exit(0);
  };
  process.on("SIGINT", () => void shutdown("SIGINT"));
  process.on("SIGTERM", () => void shutdown("SIGTERM"));
}

function parseListen(s: string): [string, number] {
  if (s.startsWith(":")) {
    return ["0.0.0.0", Number(s.slice(1))];
  }
  const [h, p] = s.split(":");
  return [h ?? "0.0.0.0", Number(p ?? "9100")];
}

main().catch((err: unknown) => {
  console.error("daydream-gateway: fatal:", err);
  process.exit(1);
});
