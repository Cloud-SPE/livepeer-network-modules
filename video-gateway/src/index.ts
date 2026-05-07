import { createCustomerPortal } from "@livepeer-rewrite/customer-portal";
import { drizzle } from "drizzle-orm/node-postgres";
import pg from "pg";

import { loadConfig } from "./config.js";
import { createDb } from "./db/pool.js";
import {
  createAssetRepo,
  createLiveStreamRepo,
  createRecordingRepo,
  createWebhookDeliveryRepo,
  createWebhookEndpointRepo,
} from "./repo/index.js";
import { createRtmpListener } from "./runtime/rtmp/listener.js";
import { buildServer } from "./server.js";

async function main(): Promise<void> {
  const cfg = loadConfig();

  const portalPool = new pg.Pool({ connectionString: cfg.databaseUrl, max: 10 });
  const portal = createCustomerPortal({
    db: drizzle(portalPool),
    pepper: cfg.customerPortalPepper,
  });

  const videoDb = createDb(cfg.databaseUrl);
  const repos = {
    assets: createAssetRepo(videoDb),
    liveStreams: createLiveStreamRepo(videoDb),
    webhookEndpoints: createWebhookEndpointRepo(videoDb),
    webhookDeliveries: createWebhookDeliveryRepo(videoDb),
    recordings: createRecordingRepo(videoDb),
  };

  const app = buildServer({ cfg });
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
