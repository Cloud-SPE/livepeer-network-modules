import { loadConfig } from "./config.js";
import { buildServer } from "./server.js";

async function main(): Promise<void> {
  const config = loadConfig();
  const built = await buildServer(config);

  const shutdown = async (signal: string) => {
    built.app.log.info({ signal }, "shutting down");
    try {
      await built.close();
      process.exit(0);
    } catch (err) {
      built.app.log.error({ err }, "shutdown failed");
      process.exit(1);
    }
  };

  process.once("SIGINT", () => void shutdown("SIGINT"));
  process.once("SIGTERM", () => void shutdown("SIGTERM"));

  await built.app.listen({
    host: config.listen.host,
    port: config.listen.port,
  });
}

void main().catch((err) => {
  console.error("fatal: failed to start daydream-portal", err);
  process.exit(1);
});
