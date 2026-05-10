import Fastify from "fastify";
import type { FastifyInstance } from "fastify";
import { admin as portalAdmin } from "@livepeer-rewrite/customer-portal";
import type { CustomerPortal } from "@livepeer-rewrite/customer-portal";
import type { Db } from "@livepeer-rewrite/customer-portal/db";

import type { Config } from "./config.js";
import { registerChatCompletions } from "./routes/chat-completions.js";
import { registerEmbeddings } from "./routes/embeddings.js";
import { registerAudioTranscriptions } from "./routes/audio-transcriptions.js";
import { registerAudioSpeech } from "./routes/audio-speech.js";
import { registerImagesGenerations } from "./routes/images-generations.js";
import { registerRealtime } from "./routes/realtime.js";
import { registerCustomerPortalRoutes } from "./routes/customer-portal.js";
import { registerOperatorRoutes } from "./routes/operator.js";
import { registerModelsRoute } from "./routes/models.js";
import { registerStripeRoutes } from "./routes/stripe.js";
import { defaultAdminDist, defaultPortalDist, registerSpaStatic } from "./runtime/static.js";
import { createRouteSelector } from "./service/routeSelector.js";
import type { Queryable } from "./repo/rateCard.js";

type RateCardStore = Queryable & {
  connect?: () => Promise<{
    query: (sql: string, args?: unknown[]) => Promise<{ rows: Record<string, unknown>[] }>;
    release: () => void;
  }>;
};

export interface BuildServerDeps {
  cfg: Config;
  db?: Db;
  portal?: CustomerPortal;
  rateCardStore?: RateCardStore;
}

export async function buildServer(input: BuildServerDeps): Promise<FastifyInstance> {
  const { cfg } = input;
  const app = Fastify({
    logger: { level: process.env["LOG_LEVEL"] ?? "info" },
    bodyLimit: 100 * 1024 * 1024,
  });
  const routeSelector = createRouteSelector(cfg);

  app.addContentTypeParser(
    /^multipart\/form-data/,
    { parseAs: "buffer" },
    (_req, body, done) => {
      done(null, body);
    },
  );

  app.get("/healthz", async (_req, reply) => {
    await reply.code(200).header("Content-Type", "text/plain").send("ok\n");
  });

  registerChatCompletions(
    app,
    cfg,
    routeSelector,
    input.portal && input.rateCardStore
      ? {
          authResolver: input.portal.authResolver,
          uiAuthResolver: input.portal.uiAuthResolver,
          wallet: input.portal.wallet,
          rateCardStore: input.rateCardStore,
        }
      : undefined,
  );
  registerEmbeddings(
    app,
    cfg,
    routeSelector,
    input.portal && input.rateCardStore
      ? {
          authResolver: input.portal.authResolver,
          uiAuthResolver: input.portal.uiAuthResolver,
          wallet: input.portal.wallet,
          rateCardStore: input.rateCardStore,
        }
      : undefined,
  );
  registerAudioTranscriptions(
    app,
    cfg,
    routeSelector,
    input.portal && input.rateCardStore
      ? {
          authResolver: input.portal.authResolver,
          uiAuthResolver: input.portal.uiAuthResolver,
          wallet: input.portal.wallet,
          rateCardStore: input.rateCardStore,
        }
      : undefined,
  );
  registerAudioSpeech(
    app,
    cfg,
    routeSelector,
    input.portal && input.rateCardStore
      ? {
          authResolver: input.portal.authResolver,
          uiAuthResolver: input.portal.uiAuthResolver,
          wallet: input.portal.wallet,
          rateCardStore: input.rateCardStore,
        }
      : undefined,
  );
  if (input.db && input.portal) {
    registerCustomerPortalRoutes(app, {
      db: input.db,
      portal: input.portal,
      authPepper: cfg.authPepper,
      routeSelector,
    });
    registerModelsRoute(app, input.portal.authResolver, routeSelector);
    registerStripeRoutes(app, {
      cfg,
      db: input.db,
      portal: input.portal,
    });
  }
  if (input.portal?.adminAuthResolver) {
    portalAdmin.registerAdminRoutes(app, {
      db: input.db!,
      engine: input.portal.adminEngine,
      authResolver: input.portal.adminAuthResolver,
      customerTokenService: input.portal.customerTokenService,
      issueApiKey: input.portal.issueApiKey,
      revokeApiKey: input.portal.revokeApiKey,
    });
    if (input.rateCardStore) {
      registerOperatorRoutes(app, {
        authResolver: input.portal.adminAuthResolver,
        rateCardStore: input.rateCardStore,
        routeSelector,
      });
    }
  }
  registerImagesGenerations(
    app,
    cfg,
    routeSelector,
    input.portal && input.rateCardStore
      ? {
          authResolver: input.portal.authResolver,
          uiAuthResolver: input.portal.uiAuthResolver,
          wallet: input.portal.wallet,
          rateCardStore: input.rateCardStore,
        }
      : undefined,
  );
  void app.register(registerRealtime, { cfg, routeSelector });

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
