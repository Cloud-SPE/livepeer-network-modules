import { existsSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { createCustomerPortal, db as portalDb } from "@livepeer-rewrite/customer-portal";
import { registerCustomerSelfServiceRoutes } from "@livepeer-rewrite/customer-portal/routes";

import { loadConfig } from "./config.js";
import { createDb } from "./db/pool.js";
import { createLiveSessionDirectory } from "./livepeer/liveSessionDirectory.js";
import { createRouteSelector } from "./livepeer/routeSelector.js";
import {
  createAssetRepo,
  createLiveStreamRepo,
  createRecordingRepo,
  createWebhookDeliveryRepo,
  createWebhookEndpointRepo,
  createWebhookFailureRepo,
} from "./repo/index.js";
import { createRtmpListener } from "./runtime/rtmp/listener.js";
import { buildServer } from "./server.js";
import { createRetryDispatcher } from "./service/webhookDispatcher.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const VIDEO_GATEWAY_ROOT = resolve(__dirname, "..");
const REPO_ROOT = resolve(VIDEO_GATEWAY_ROOT, "..");

function resolveCustomerPortalMigrationsDir(): string {
  const packagedPath = resolve(VIDEO_GATEWAY_ROOT, "customer-portal-migrations");
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

  const videoDb = createDb(cfg.databaseUrl);
  const repos = {
    assets: createAssetRepo(videoDb),
    liveStreams: createLiveStreamRepo(videoDb),
    webhookEndpoints: createWebhookEndpointRepo(videoDb),
    webhookDeliveries: createWebhookDeliveryRepo(videoDb),
    webhookFailures: createWebhookFailureRepo(videoDb),
    recordings: createRecordingRepo(videoDb),
  };

  const dispatcher = createRetryDispatcher(
    { pepper: cfg.webhookHmacPepper },
    {
      endpoints: repos.webhookEndpoints,
      deliveries: repos.webhookDeliveries,
      failures: repos.webhookFailures,
    },
  );
  const liveSessions = createLiveSessionDirectory();
  const routeSelector = createRouteSelector({
    brokerUrl: cfg.brokerUrl,
    resolverSocket: cfg.resolverSocket,
    resolverProtoRoot: cfg.resolverProtoRoot,
    resolverSnapshotTtlMs: cfg.resolverSnapshotTtlMs,
  });

  const app = buildServer({
    cfg,
    db: portalDbConn,
    portal,
    routeSelector,
    liveSessions,
    admin: {
      videoDb,
      assets: repos.assets,
      liveStreamsRepo: repos.liveStreams,
      recordingsRepo: repos.recordings,
      failures: repos.webhookFailures,
      dispatcher,
    },
  });
  registerCustomerSelfServiceRoutes(app, {
    db: portalDbConn,
    portal,
    authPepper: cfg.customerPortalPepper,
  });
  const rtmp = createRtmpListener({ cfg });
  app.log.info(
    {
      portal_auth: typeof portal.authResolver,
      video_db: typeof videoDb,
      repos: Object.keys(repos).length,
      rtmp_addr: cfg.rtmpListenAddr,
    },
    "video-gateway booting",
  );

  try {
    await app.listen({ host: "0.0.0.0", port: cfg.listenPort });
  } catch (err) {
    app.log.error(err, "failed to listen");
    rtmp.close();
    process.exit(1);
  }
}

void main();
