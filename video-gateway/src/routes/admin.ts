import { desc, eq, isNull } from "drizzle-orm";
import type { FastifyInstance, FastifyReply, FastifyRequest, preHandlerAsyncHookHandler } from "fastify";
import type { AdminAuthResolver } from "@livepeer-rewrite/customer-portal/auth";

import type { Db as VideoDb } from "../db/pool.js";
import type { LiveSessionDirectory } from "../livepeer/liveSessionDirectory.js";
import type { VideoRouteSelector } from "../livepeer/routeSelector.js";
import { assets, liveStreams, recordings } from "../db/schema.js";
import type { AssetRepo, LiveStreamRepo, RecordingRepo, WebhookFailureRepo, PlaybackIdRepo, MutableEncodingJobRepo, MutableRenditionRepo } from "../repo/index.js";
import type { RetryDispatcher } from "../service/webhookDispatcher.js";
import type { AbrExecutionManager } from "../service/abrExecution.js";
import { maybeHandoffRecording } from "./live-streams.js";

declare module "fastify" {
  interface FastifyRequest {
    adminActor?: string;
  }
}

export interface AdminRoutesDeps {
  authResolver: AdminAuthResolver;
  videoDb: VideoDb;
  routeSelector: VideoRouteSelector;
  liveSessions: LiveSessionDirectory;
  assets: AssetRepo;
  liveStreamsRepo: LiveStreamRepo;
  recordingsRepo: RecordingRepo;
  playbackIds?: PlaybackIdRepo;
  jobsRepo?: MutableEncodingJobRepo;
  renditionsRepo?: MutableRenditionRepo;
  failures: WebhookFailureRepo;
  dispatcher: RetryDispatcher;
  execution?: AbrExecutionManager;
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
    const playbackByAssetId = new Map<string, string>();
    if (deps.playbackIds) {
      for (const row of rows) {
        const [playback] = await deps.playbackIds.byAsset(row.id);
        if (playback) playbackByAssetId.set(row.id, playback.id);
      }
    }
    await reply.code(200).send({
      items: await Promise.all(rows.map(async (row) => ({
        id: row.id,
        projectId: row.projectId,
        status: row.status,
        durationSec: row.durationSec !== null ? Number(row.durationSec) : null,
        createdAt: row.createdAt.toISOString(),
        deletedAt: row.deletedAt?.toISOString() ?? null,
        playbackId: playbackByAssetId.get(row.id) ?? null,
        playbackUrl: playbackByAssetId.has(row.id)
          ? `/v1/playback/${encodeURIComponent(playbackByAssetId.get(row.id)!)}`
          : null,
        renditions: deps.renditionsRepo ? (await deps.renditionsRepo.byAsset(row.id)).map((rendition) => ({
          id: rendition.id,
          resolution: rendition.resolution,
          codec: rendition.codec,
          status: rendition.status,
        })) : [],
        jobs: deps.jobsRepo ? (await deps.jobsRepo.byAsset(row.id)).map((job) => ({
          id: job.id,
          kind: job.kind,
          status: job.status,
          errorMessage: job.errorMessage ?? null,
        })) : [],
      }))),
    });
  });

  app.delete<{ Params: { id: string } }>("/admin/assets/:id", { preHandler }, async (req, reply) => {
    await deps.assets.softDelete(req.params.id, new Date());
    await reply.code(204).send();
  });

  app.post<{ Params: { id: string } }>("/admin/assets/:id/restore", { preHandler }, async (req, reply) => {
    const rows = await deps.videoDb.select().from(assets).where(eq(assets.id, req.params.id)).limit(1);
    const asset = rows[0];
    if (!asset) {
      await reply.code(404).send({ error: "not_found" });
      return;
    }
    await deps.videoDb
      .update(assets)
      .set({ deletedAt: null, status: restoredAssetStatus(asset) })
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
        name: row.name ?? row.id,
        projectId: row.projectId,
        status: adminStreamStatus(row.status),
        sessionId: row.sessionId,
        playbackUrl:
          row.sessionId !== null
            ? deps.liveSessions.get(row.sessionId)?.hlsPlaybackUrl ?? null
            : deps.liveSessions.getByStreamId(row.id)?.hlsPlaybackUrl ?? null,
        startedAt: row.createdAt.toISOString(),
        endedAt: row.endedAt?.toISOString() ?? null,
        viewerCount: null,
        recordToVod: row.recordingEnabled,
      })),
    });
  });

  app.post<{ Params: { id: string } }>("/admin/live-streams/:id/end", { preHandler }, async (req, reply) => {
    const stream = await deps.liveStreamsRepo.byId(req.params.id);
    if (!stream) {
      await reply.code(404).send({ error: "not_found" });
      return;
    }
    const endedAt = new Date();
    await deps.liveStreamsRepo.updateStatus(req.params.id, "ended", {
      endedAt,
      lastSeenAt: endedAt,
    });
    const handoff = await maybeHandoffRecording(deps, {
      liveStreamId: stream.id,
      projectId: stream.projectId,
      sessionId: stream.sessionId,
      endedAt,
    });
    await reply.code(200).send({
      ok: true,
      recording_asset_id: handoff?.assetId ?? stream.recordingAssetId ?? null,
      recording_execution_id: handoff?.executionId ?? null,
    });
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

function restoredAssetStatus(asset: { readyAt?: Date | null; selectedOffering?: string | null }): "ready" | "queued" | "preparing" {
  if (asset.readyAt) return "ready";
  if (asset.selectedOffering) return "queued";
  return "preparing";
}

function adminStreamStatus(status: string): string {
  if (status === "active" || status === "reconnecting") return "live";
  return status;
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
