import Fastify from "fastify";
import type { FastifyInstance } from "fastify";
import { admin as portalAdmin } from "@livepeer-rewrite/customer-portal";
import type { CustomerPortal } from "@livepeer-rewrite/customer-portal";
import type { Db as PortalDb } from "@livepeer-rewrite/customer-portal/db";

import type { Config } from "./config.js";
import type { StorageProvider } from "./engine/interfaces/storageProvider.js";
import type { LiveSessionDirectory } from "./livepeer/liveSessionDirectory.js";
import type { VideoRouteSelector } from "./livepeer/routeSelector.js";
import type {
  AssetRepo,
  LiveStreamRepo,
  PlaybackIdRepo,
  RecordingRepo,
  UsageRecordRepo,
  WebhookFailureRepo,
  MutableEncodingJobRepo,
  MutableRenditionRepo,
} from "./repo/index.js";
import type { Db as VideoDb } from "./db/pool.js";
import { registerAdmin } from "./routes/admin.js";
import { registerLiveStreams } from "./routes/live-streams.js";
import { registerUploads } from "./routes/uploads.js";
import { registerVod } from "./routes/vod.js";
import { registerPlayback } from "./routes/playback.js";
import { registerProjects } from "./routes/projects.js";
import { registerVideoCustomerPortalRoutes } from "./routes/customer-portal.js";
import { registerWebhooks } from "./routes/webhooks.js";
import { defaultAdminDist, defaultPortalDist, registerSpaStatic } from "./runtime/static.js";
import type { AbrExecutionManager } from "./service/abrExecution.js";
import type { UsageLedger } from "./service/usageLedger.js";
import type { RetryDispatcher } from "./service/webhookDispatcher.js";
import { getProjectById } from "./service/projects.js";
import { uploads } from "./db/schema.js";
import { eq } from "drizzle-orm";

export interface BuildServerInput {
  cfg: Config;
  routeSelector: VideoRouteSelector;
  liveSessions: LiveSessionDirectory;
  db?: PortalDb;
  portal?: CustomerPortal;
  admin?: {
    videoDb: VideoDb;
    assets: AssetRepo;
    liveStreamsRepo: LiveStreamRepo;
    jobsRepo: MutableEncodingJobRepo;
    renditionsRepo: MutableRenditionRepo;
    playbackIds: PlaybackIdRepo;
    recordingsRepo: RecordingRepo;
    usageRecords: UsageRecordRepo;
    failures: WebhookFailureRepo;
    dispatcher: RetryDispatcher;
    execution: AbrExecutionManager;
    usageLedger: UsageLedger;
    storage: StorageProvider;
  };
}

export async function buildServer(input: BuildServerInput): Promise<FastifyInstance> {
  const { cfg } = input;
  const app = Fastify({
    logger: { level: process.env["LOG_LEVEL"] ?? "info" },
    bodyLimit: 100 * 1024 * 1024,
  });

  app.get("/healthz", async (_req, reply) => {
    await reply.code(200).header("Content-Type", "text/plain").send("ok\n");
  });

  registerLiveStreams(app, {
    cfg,
    routeSelector: input.routeSelector,
    liveSessions: input.liveSessions,
    liveStreamsRepo: input.admin?.liveStreamsRepo,
    recordingsRepo: input.admin?.recordingsRepo,
    execution: input.admin?.execution,
    usageLedger: input.admin?.usageLedger,
    projectExists: input.admin?.videoDb
      ? async (projectId: string) => (await getProjectById(input.admin!.videoDb, projectId)) !== null
      : undefined,
  });
  if (input.admin?.videoDb) {
    registerUploads(app, {
      cfg,
      videoDb: input.admin.videoDb,
      uploadExists: async (id: string) => {
        const rows = await input.admin!.videoDb.select().from(uploads).where(eq(uploads.id, id)).limit(1);
        return rows.length > 0;
      },
      completeUpload: async (id: string) => {
        const rows = await input.admin!.videoDb
          .update(uploads)
          .set({
            status: "completed",
            completedAt: new Date(),
          })
          .where(eq(uploads.id, id))
          .returning({ id: uploads.id });
        return rows.length > 0;
      },
    });
  } else {
    registerUploads(app, { cfg, videoDb: input.admin?.videoDb });
  }
  registerVod(app, {
    routeSelector: input.routeSelector,
    assetsRepo: input.admin?.assets,
    renditionsRepo: input.admin?.renditionsRepo,
    jobsRepo: input.admin?.jobsRepo,
    playbackIds: input.admin?.playbackIds,
    execution: input.admin?.execution,
    usageLedger: input.admin?.usageLedger,
  });
  registerPlayback(app, {
    cfg,
    liveSessions: input.liveSessions,
    assetsRepo: input.admin?.assets,
    playbackIds: input.admin?.playbackIds,
    storage: input.admin?.storage,
  });
  registerProjects(app, { videoDb: input.admin?.videoDb });
  registerWebhooks(app);
  if (input.portal?.adminAuthResolver && input.db) {
    portalAdmin.registerAdminRoutes(app, {
      db: input.db,
      engine: input.portal.adminEngine,
      authResolver: input.portal.adminAuthResolver,
      customerTokenService: input.portal.customerTokenService,
      issueApiKey: input.portal.issueApiKey,
      revokeApiKey: input.portal.revokeApiKey,
    });
  }
  if (input.portal) {
    registerVideoCustomerPortalRoutes(app, {
      portal: input.portal,
      cfg,
      routeSelector: input.routeSelector,
      liveSessions: input.liveSessions,
      videoDb: input.admin?.videoDb,
      assetsRepo: input.admin?.assets,
      liveStreamsRepo: input.admin?.liveStreamsRepo,
      recordingsRepo: input.admin?.recordingsRepo,
      playbackIds: input.admin?.playbackIds,
      jobsRepo: input.admin?.jobsRepo,
      renditionsRepo: input.admin?.renditionsRepo,
      execution: input.admin?.execution,
      usageRecords: input.admin?.usageRecords,
      usageLedger: input.admin?.usageLedger,
    });
  }
  if (input.admin && input.portal?.adminAuthResolver) {
    registerAdmin(app, {
      ...input.admin,
      authResolver: input.portal.adminAuthResolver,
      routeSelector: input.routeSelector,
      liveSessions: input.liveSessions,
    });
  }

  await registerSpaStatic(app, {
    rootDir: defaultPortalDist(),
    prefix: "/portal/",
    label: "portal",
  });

  await registerSpaStatic(app, {
    rootDir: defaultAdminDist(),
    prefix: "/admin/console/",
    label: "admin console",
  });

  return app;
}
