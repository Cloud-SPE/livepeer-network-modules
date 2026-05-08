import { existsSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { createCustomerPortal, db as portalDb } from "@livepeer-rewrite/customer-portal";
import { billing } from "@livepeer-rewrite/customer-portal";
import { loadConfig } from "./config.js";
import * as payment from "./livepeer/payment.js";
import { buildServer } from "./server.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const require = createRequire(import.meta.url);
const OPENAI_GATEWAY_ROOT = resolve(__dirname, "..");
const REPO_ROOT = resolve(OPENAI_GATEWAY_ROOT, "..");

function resolveCustomerPortalMigrationsDir(): string {
  const localRepoPath = resolve(REPO_ROOT, "customer-portal", "migrations");
  if (existsSync(localRepoPath)) return localRepoPath;

  const pkgEntry = require.resolve("@livepeer-rewrite/customer-portal/db");
  const pkgRoot = resolve(dirname(pkgEntry), "..", "..");
  const pkgMigrations = resolve(pkgRoot, "migrations");
  if (existsSync(pkgMigrations)) return pkgMigrations;

  throw new Error(
    `could not locate customer-portal migrations; checked ${localRepoPath} and ${pkgMigrations}`,
  );
}

async function main(): Promise<void> {
  const cfg = loadConfig();
  const pool = portalDb.createPool({ connectionString: cfg.databaseUrl });
  const db = portalDb.makeDb(pool);
  await portalDb.runMigrations(db, resolveCustomerPortalMigrationsDir());
  await portalDb.runMigrations(db, resolve(OPENAI_GATEWAY_ROOT, "migrations"));
  const portal = createCustomerPortal({
    db,
    pepper: cfg.authPepper,
    ...(cfg.stripe
      ? {
          stripe: billing.stripe.createSdkStripeClient({
            secretKey: cfg.stripe.secretKey,
            webhookSecret: cfg.stripe.webhookSecret,
          }),
        }
      : {}),
    admin:
      cfg.adminUser && cfg.adminPass
        ? {
            user: cfg.adminUser,
            pass: cfg.adminPass,
            realm: "openai-gateway-admin",
          }
        : undefined,
  });

  // Dial the local payer-daemon. Routes call payment.buildPayment per
  // request, which uses this connection.
  await payment.init({ socketPath: cfg.payerDaemonSocket, protoRoot: cfg.paymentProtoRoot });

  const app = await buildServer({ cfg, db, portal, rateCardStore: pool });
  try {
    await app.listen({ host: "0.0.0.0", port: cfg.listenPort });
  } catch (err) {
    app.log.error(err, "failed to listen");
    payment.shutdown();
    await pool.end().catch(() => undefined);
    process.exit(1);
  }
}

void main();
