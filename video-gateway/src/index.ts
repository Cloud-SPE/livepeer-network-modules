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
  createEncodingJobRepo,
    createLiveStreamRepo,
  createPlaybackIdRepo,
  createRecordingRepo,
  createRenditionRepo,
  createLiveSessionDebitRepo,
  createUsageRecordRepo,
  createWebhookDeliveryRepo,
  createWebhookEndpointRepo,
  createWebhookFailureRepo,
} from "./repo/index.js";
import { createRtmpListener } from "./runtime/rtmp/listener.js";
import { buildServer } from "./server.js";
import { createAbrExecutionManager } from "./service/abrExecution.js";
import { createUsageLedger } from "./service/usageLedger.js";
import { createRetryDispatcher } from "./service/webhookDispatcher.js";
import { createS3StorageProvider, loadS3ConfigFromEnv } from "./storage/index.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const VIDEO_GATEWAY_ROOT = resolve(__dirname, "..");
const REPO_ROOT = resolve(VIDEO_GATEWAY_ROOT, "..");

function firstExistingPath(paths: string[]): string | null {
  for (const candidate of paths) {
    if (existsSync(candidate)) return candidate;
  }
  return null;
}

export function resolveCustomerPortalMigrationsDir(): string {
  const candidates = [
    resolve(VIDEO_GATEWAY_ROOT, "customer-portal-migrations"),
    resolve(VIDEO_GATEWAY_ROOT, "..", "customer-portal", "migrations"),
    resolve(VIDEO_GATEWAY_ROOT, "..", "..", "customer-portal", "migrations"),
    resolve(REPO_ROOT, "customer-portal", "migrations"),
  ];
  const resolvedPath = firstExistingPath(candidates);
  if (resolvedPath) return resolvedPath;
  throw new Error(`could not locate customer-portal migrations; checked ${candidates.join(", ")}`);
}

export function resolveVideoGatewayMigrationsDir(): string {
  const candidates = [
    resolve(VIDEO_GATEWAY_ROOT, "migrations"),
    resolve(VIDEO_GATEWAY_ROOT, "..", "migrations"),
  ];
  const resolvedPath = firstExistingPath(candidates);
  if (resolvedPath) return resolvedPath;
  throw new Error(`could not locate video-gateway migrations; checked ${candidates.join(", ")}`);
}

export async function main(): Promise<void> {
  const cfg = loadConfig();

  const portalPool = portalDb.createPool({ connectionString: cfg.databaseUrl, max: 10 });
  const portalDbConn = portalDb.makeDb(portalPool);
  await portalDb.runMigrations(portalDbConn, resolveCustomerPortalMigrationsDir());
  await portalDb.runMigrations(portalDbConn, resolveVideoGatewayMigrationsDir());
  const portal = createCustomerPortal({
    db: portalDbConn,
    pepper: cfg.customerPortalPepper,
    admin: cfg.adminTokens.length > 0 ? { tokens: cfg.adminTokens } : undefined,
  });

  const videoDb = createDb(cfg.databaseUrl);
  const repos = {
    assets: createAssetRepo(videoDb),
    jobs: createEncodingJobRepo(videoDb),
    renditions: createRenditionRepo(videoDb),
    playbackIds: createPlaybackIdRepo(videoDb),
    liveStreams: createLiveStreamRepo(videoDb),
    liveSessionDebits: createLiveSessionDebitRepo(videoDb),
    usageRecords: createUsageRecordRepo(videoDb),
    webhookEndpoints: createWebhookEndpointRepo(videoDb),
    webhookDeliveries: createWebhookDeliveryRepo(videoDb),
    webhookFailures: createWebhookFailureRepo(videoDb),
    recordings: createRecordingRepo(videoDb),
  };
  const storage = createS3StorageProvider(loadS3ConfigFromEnv());

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
  const usageLedger = createUsageLedger({
    portalDb: portalDbConn,
    videoDb,
    usageRecords: repos.usageRecords,
    liveSessionDebits: repos.liveSessionDebits,
  });
  const execution = createAbrExecutionManager({
    assets: repos.assets,
    jobs: repos.jobs,
    renditions: repos.renditions,
    playbackIds: repos.playbackIds,
    recordings: repos.recordings,
    liveStreams: repos.liveStreams,
    storage,
    routeSelector,
    usageLedger,
    logger: console,
  });

  const app = await buildServer({
    cfg,
    db: portalDbConn,
    portal,
    routeSelector,
    liveSessions,
    admin: {
      videoDb,
      assets: repos.assets,
      jobsRepo: repos.jobs,
      renditionsRepo: repos.renditions,
      playbackIds: repos.playbackIds,
      liveStreamsRepo: repos.liveStreams,
      recordingsRepo: repos.recordings,
      usageRecords: repos.usageRecords,
      failures: repos.webhookFailures,
      dispatcher,
      execution,
      usageLedger,
      storage,
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

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  void main();
}
