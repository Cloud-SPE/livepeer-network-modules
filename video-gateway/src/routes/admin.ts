import { desc, eq, isNull } from "drizzle-orm";
import type { FastifyInstance, FastifyReply, FastifyRequest, preHandlerAsyncHookHandler } from "fastify";
import type { AdminAuthResolver } from "@livepeer-rewrite/customer-portal/auth";

import type { Db as VideoDb } from "../db/pool.js";
import type { VideoRouteSelector } from "../livepeer/routeSelector.js";
import { assets, liveStreams, recordings } from "../db/schema.js";
import type { AssetRepo, LiveStreamRepo, RecordingRepo, WebhookFailureRepo } from "../repo/index.js";
import type { RetryDispatcher } from "../service/webhookDispatcher.js";

declare module "fastify" {
  interface FastifyRequest {
    adminActor?: string;
  }
}

export interface AdminRoutesDeps {
  authResolver: AdminAuthResolver;
  videoDb: VideoDb;
  routeSelector: VideoRouteSelector;
  assets: AssetRepo;
  liveStreamsRepo: LiveStreamRepo;
  recordingsRepo: RecordingRepo;
  failures: WebhookFailureRepo;
  dispatcher: RetryDispatcher;
}

export function registerAdmin(app: FastifyInstance, deps: AdminRoutesDeps): void {
  const preHandler = adminAuthPreHandler(deps.authResolver);

  app.get("/admin/video/resolver-candidates", { preHandler }, async (_req, reply) => {
    const candidates = await deps.routeSelector.inspect();
    await reply.code(200).send({ candidates });
  });

  app.get("/admin/assets", { preHandler }, async (req, reply) => {
    const query = req.query as Record<string, string | undefined>;
    const includeDeleted = query["include_deleted"] === "true";
    const rows = await deps.videoDb
      .select()
      .from(assets)
      .where(includeDeleted ? undefined : isNull(assets.deletedAt))
      .orderBy(desc(assets.createdAt))
      .limit(100);
    await reply.code(200).send({
      items: rows.map((row) => ({
        id: row.id,
        projectId: row.projectId,
        status: row.status,
        durationSec: row.durationSec !== null ? Number(row.durationSec) : null,
        createdAt: row.createdAt.toISOString(),
        deletedAt: row.deletedAt?.toISOString() ?? null,
      })),
    });
  });

  app.delete<{ Params: { id: string } }>("/admin/assets/:id", { preHandler }, async (req, reply) => {
    await deps.assets.softDelete(req.params.id, new Date());
    await reply.code(204).send();
  });

  app.post<{ Params: { id: string } }>("/admin/assets/:id/restore", { preHandler }, async (req, reply) => {
    await deps.videoDb
      .update(assets)
      .set({ deletedAt: null, status: "ready" })
      .where(eq(assets.id, req.params.id));
    await reply.code(204).send();
  });

  app.get("/admin/live-streams", { preHandler }, async (_req, reply) => {
    const rows = await deps.videoDb
      .select()
      .from(liveStreams)
      .orderBy(desc(liveStreams.createdAt))
      .limit(100);
    await reply.code(200).send({
      items: rows.map((row) => ({
        id: row.id,
        projectId: row.projectId,
        status: row.status,
        startedAt: row.createdAt.toISOString(),
        endedAt: row.endedAt?.toISOString() ?? null,
        viewerCount: null,
        recordToVod: row.recordingEnabled,
      })),
    });
  });

  app.post<{ Params: { id: string } }>("/admin/live-streams/:id/end", { preHandler }, async (req, reply) => {
    await deps.liveStreamsRepo.updateStatus(req.params.id, "ended", { endedAt: new Date() });
    await reply.code(200).send({ ok: true });
  });

  app.get("/admin/recordings", { preHandler }, async (_req, reply) => {
    const rows = await deps.videoDb
      .select()
      .from(recordings)
      .orderBy(desc(recordings.createdAt))
      .limit(100);
    await reply.code(200).send({
      items: rows.map((row) => ({
        id: row.id,
        streamId: row.liveStreamId,
        assetId: row.assetId,
        status: row.status,
        startedAt: row.startedAt?.toISOString() ?? row.createdAt.toISOString(),
        endedAt: row.endedAt?.toISOString() ?? null,
        durationSec:
          row.startedAt && row.endedAt
            ? Math.max(0, (row.endedAt.getTime() - row.startedAt.getTime()) / 1000)
            : null,
      })),
    });
  });

  app.get("/admin/webhook-failures", { preHandler }, async (req, reply) => {
    const query = req.query as Record<string, string | undefined>;
    const limit = Math.min(parseInt(query["limit"] ?? "50", 10) || 50, 200);
    const endpointId = query["endpoint_id"];
    const list = await deps.failures.list(
      endpointId ? { endpointId, limit } : { limit },
    );
    await reply.code(200).send({ items: list });
  });

  app.post("/admin/webhook-failures/:id/replay", { preHandler }, async (req, reply) => {
    const { id } = req.params as { id: string };
    try {
      const out = await deps.dispatcher.replayFailure(id);
      await reply.code(out.delivered ? 200 : 502).send({
        delivered: out.delivered,
        attempts: out.attempts,
        final_status: out.finalStatus,
        last_error: out.lastError,
      });
    } catch (err) {
      const msg = err instanceof Error ? err.message : "replay_failed";
      await reply.code(404).send({ error: msg });
    }
  });
}

function adminAuthPreHandler(
  resolver: AdminAuthResolver,
): preHandlerAsyncHookHandler {
  return async (req: FastifyRequest, reply: FastifyReply): Promise<void> => {
    const result = await resolver.resolve({
      headers: req.headers as Record<string, string | undefined>,
      ip: req.ip,
    });
    if (!result) {
      await reply.code(401).send({
        error: { code: "authentication_failed", message: "admin token + actor required", type: "AdminAuthError" },
      });
      return;
    }
    req.adminActor = result.actor;
  };
}
