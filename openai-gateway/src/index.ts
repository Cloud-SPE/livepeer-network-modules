import { loadConfig } from "./config.js";
import * as payment from "./livepeer/payment.js";
import { buildServer } from "./server.js";

async function main(): Promise<void> {
  const cfg = loadConfig();

  // Dial the local payer-daemon. Routes call payment.buildPayment per
  // request, which uses this connection.
  await payment.init({ socketPath: cfg.payerDaemonSocket, protoRoot: cfg.paymentProtoRoot });

  const app = buildServer(cfg);
  try {
    await app.listen({ host: "0.0.0.0", port: cfg.listenPort });
  } catch (err) {
    app.log.error(err, "failed to listen");
    payment.shutdown();
    process.exit(1);
  }
}

void main();
