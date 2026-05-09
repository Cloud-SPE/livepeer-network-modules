import Fastify from "fastify";
import type { FastifyInstance } from "fastify";
import { admin as portalAdmin } from "@livepeer-rewrite/customer-portal";
import type { CustomerPortal } from "@livepeer-rewrite/customer-portal";
import type { Db as PortalDb } from "@livepeer-rewrite/customer-portal/db";

import type { Config } from "./config.js";
import type { LiveSessionDirectory } from "./livepeer/liveSessionDirectory.js";
import type { VideoRouteSelector } from "./livepeer/routeSelector.js";
import type { AssetRepo, LiveStreamRepo, RecordingRepo, WebhookFailureRepo } from "./repo/index.js";
import type { Db as VideoDb } from "./db/pool.js";
import { registerAdmin } from "./routes/admin.js";
import { registerLiveStreams } from "./routes/live-streams.js";
import { registerUploads } from "./routes/uploads.js";
import { registerVod } from "./routes/vod.js";
import { registerPlayback } from "./routes/playback.js";
import { registerProjects } from "./routes/projects.js";
import { registerWebhooks } from "./routes/webhooks.js";
import type { RetryDispatcher } from "./service/webhookDispatcher.js";

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
    recordingsRepo: RecordingRepo;
    failures: WebhookFailureRepo;
    dispatcher: RetryDispatcher;
  };
}

export function buildServer(input: BuildServerInput): FastifyInstance {
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
  });
  registerUploads(app, { cfg });
  registerVod(app);
  registerPlayback(app, { cfg, liveSessions: input.liveSessions });
  registerProjects(app);
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
  if (input.admin && input.portal?.adminAuthResolver) {
    registerAdmin(app, {
      ...input.admin,
      authResolver: input.portal.adminAuthResolver,
    });
  }

  return app;
}
