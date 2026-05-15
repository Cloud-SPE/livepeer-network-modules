import { and, desc, eq, inArray, isNull } from "drizzle-orm";
import type { FastifyInstance, FastifyReply, FastifyRequest, preHandlerAsyncHookHandler } from "fastify";
import type { AdminAuthResolver } from "@livepeer-network-modules/customer-portal/auth";
import { renderRouteHealthMetrics, summarizeRouteHealth } from "@livepeer-network-modules/gateway-route-health";
import { z } from "zod";

import type { Db as VideoDb } from "../db/pool.js";
import type { LiveSessionDirectory } from "../livepeer/liveSessionDirectory.js";
import type { VideoRouteSelector } from "../livepeer/routeSelector.js";
import { assets, liveStreams, projects, recordings, webhookEndpoints } from "../db/schema.js";
import type { AssetRepo, LiveStreamRepo, RecordingRepo, WebhookFailureRepo, PlaybackIdRepo, MutableEncodingJobRepo, MutableRenditionRepo } from "../repo/index.js";
import type { UsageRecordRepo } from "../repo/usage.js";
import type { RetryDispatcher } from "../service/webhookDispatcher.js";
import type { AbrExecutionManager } from "../service/abrExecution.js";
import { summarizeProjectUsage } from "../service/projects.js";
import { usageWorkId, type ChargeSummary, type UsageLedger } from "../service/usageLedger.js";
import { maybeHandoffRecording } from "./live-streams.js";

const ADMIN_STREAM_STALE_AFTER_SEC = 60;

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
  usageRecords?: UsageRecordRepo;
  failures: WebhookFailureRepo;
  dispatcher: RetryDispatcher;
  execution?: AbrExecutionManager;
  usageLedger?: UsageLedger;
}

export function registerAdmin(app: FastifyInstance, deps: AdminRoutesDeps): void {
  const preHandler = adminAuthPreHandler(deps.authResolver);
  const RouteControlBody = z.object({
    broker_url: z.string().trim().min(1),
  });
  const PlaybackPolicyBody = z.object({
    policy: z.string().trim().min(1),
    token_required: z.boolean(),
  });

  app.get("/admin/video/resolver-candidates", { preHandler }, async (_req, reply) => {
    const candidates = await deps.routeSelector.inspect();
    const suppressed = new Set(await deps.routeSelector.suppressedBrokers());
    const health = await deps.routeSelector.inspectHealth();
    const metrics = await deps.routeSelector.inspectMetrics();
    await reply.code(200).send({
      candidates: candidates.map((candidate) => ({
        ...candidate,
        suppressed: suppressed.has(candidate.brokerUrl),
      })),
      health,
      summary: summarizeRouteHealth(health),
      metrics,
    });
  });

  app.get("/admin/video/route-health/metrics", { preHandler }, async (_req, reply) => {
    const health = await deps.routeSelector.inspectHealth();
    const metrics = await deps.routeSelector.inspectMetrics();
    await reply
      .code(200)
      .header("Content-Type", "text/plain; version=0.0.4")
      .send(renderRouteHealthMetrics("video", summarizeRouteHealth(health), metrics));
  });

  app.get("/admin/video/route-controls", { preHandler }, async (_req, reply) => {
    await reply.code(200).send({
      suppressed_brokers: await deps.routeSelector.suppressedBrokers(),
    });
  });

  app.post("/admin/video/route-controls/suppress", { preHandler }, async (req, reply) => {
    const parsed = RouteControlBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.issues });
      return;
    }
    await deps.routeSelector.suppressBroker(parsed.data.broker_url);
    await reply.code(200).send({
      broker_url: parsed.data.broker_url,
      suppressed: true,
    });
  });

  app.post("/admin/video/route-controls/unsuppress", { preHandler }, async (req, reply) => {
    const parsed = RouteControlBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.issues });
      return;
    }
    await deps.routeSelector.unsuppressBroker(parsed.data.broker_url);
    await reply.code(200).send({
      broker_url: parsed.data.broker_url,
      suppressed: false,
    });
  });

  app.get("/admin/assets", { preHandler }, async (req, reply) => {
    const query = req.query as Record<string, string | undefined>;
    const includeDeleted = query["include_deleted"] === "true";
    const projectId = query["project_id"];
    const rows = await deps.videoDb
      .select()
      .from(assets)
      .where(
        projectId
          ? includeDeleted
            ? eq(assets.projectId, projectId)
            : and(eq(assets.projectId, projectId), isNull(assets.deletedAt))
          : includeDeleted
            ? undefined
            : isNull(assets.deletedAt),
      )
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

  app.post<{ Params: { id: string } }>("/admin/assets/:id/retry", { preHandler }, async (req, reply) => {
    if (!deps.execution) {
      await reply.code(501).send({ error: "execution_unavailable" });
      return;
    }
    const asset = await deps.assets.byId(req.params.id);
    if (!asset || asset.deletedAt) {
      await reply.code(404).send({ error: "asset_not_found" });
      return;
    }
    if (asset.status === "ready") {
      await reply.code(409).send({ error: "asset_already_ready" });
      return;
    }
    try {
      const out = await deps.execution.retryAsset(asset.id);
      await reply.code(202).send({
        asset_id: asset.id,
        status: "queued",
        execution_id: out.executionId,
      });
    } catch (err) {
      const message = err instanceof Error ? err.message : "retry_failed";
      await reply.code(502).send({ error: "retry_failed", message });
    }
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
    const query = _req.query as Record<string, string | undefined>;
    const projectId = query["project_id"];
    const rows = await deps.videoDb
      .select()
      .from(liveStreams)
      .where(projectId ? eq(liveStreams.projectId, projectId) : undefined)
      .orderBy(desc(liveStreams.createdAt))
      .limit(100);
    await reply.code(200).send({
      items: rows.map((row) => ({
        id: row.id,
        name: row.name ?? row.id,
        projectId: row.projectId,
        status: adminStreamStatus(row.status),
        sessionId: row.sessionId,
        playbackUrl: liveSessionForRow(deps.liveSessions, row)?.hlsPlaybackUrl ?? null,
        brokerUrl: liveSessionForRow(deps.liveSessions, row)?.brokerUrl ?? row.workerUrl ?? null,
        sessionKnown: liveSessionForRow(deps.liveSessions, row) !== null,
        lastSeenAt: row.lastSeenAt?.toISOString() ?? null,
        idleSeconds: row.lastSeenAt ? Math.max(0, Math.floor((Date.now() - row.lastSeenAt.getTime()) / 1000)) : null,
        health: adminStreamHealth(row, liveSessionForRow(deps.liveSessions, row) !== null, ADMIN_STREAM_STALE_AFTER_SEC),
        startedAt: row.createdAt.toISOString(),
        endedAt: row.endedAt?.toISOString() ?? null,
        viewerCount: null,
        recordToVod: row.recordingEnabled,
      })),
    });
  });

  app.get("/admin/usage", { preHandler }, async (req, reply) => {
    if (!deps.usageRecords) {
      await reply.code(501).send({ error: "usage_unavailable" });
      return;
    }
    const query = req.query as Record<string, string | undefined>;
    const limit = Math.min(parseInt(query["limit"] ?? "100", 10) || 100, 200);
    const customerId = query["customer_id"];
    const rows = customerId
      ? await deps.usageRecords.listByCustomer(customerId, limit)
      : await deps.usageRecords.recent(limit);
    const chargeByWorkId = deps.usageLedger
      ? await deps.usageLedger.listChargesByWorkIds(
          rows
            .map((row) => usageWorkId(row))
            .filter((workId): workId is string => workId !== null),
        )
      : new Map();
    const summary = customerId && deps.usageLedger
      ? await deps.usageLedger.summarizeCustomer(customerId)
      : {
          topupTotalCents: 0,
          usageCommittedCents: rows.reduce((sum, row) => sum + row.amountCents, 0),
          reservedOpenCents: 0,
          refundedCents: 0,
        };
    await reply.code(200).send({
      items: rows.map((row) => ({
        id: row.id,
        project_id: row.projectId,
        asset_id: row.assetId,
        live_stream_id: row.liveStreamId,
        work_id: usageWorkId(row),
        capability: row.capability,
        amount_cents: row.amountCents,
        created_at: row.createdAt.toISOString(),
        charge: serializeCharge(usageWorkId(row), chargeByWorkId),
      })),
      total_amount_cents: rows.reduce((sum, row) => sum + row.amountCents, 0),
      summary: {
        topup_total_cents: summary.topupTotalCents,
        usage_committed_cents: summary.usageCommittedCents,
        reserved_open_cents: summary.reservedOpenCents,
        refunded_cents: summary.refundedCents,
      },
    });
  });

  app.get("/admin/playback", { preHandler }, async (_req, reply) => {
    if (!deps.playbackIds) {
      await reply.code(501).send({ error: "playback_repo_unavailable" });
      return;
    }
    const rows = await deps.playbackIds.recent(100);
    await reply.code(200).send({
      items: rows.map((row) => ({
        id: row.id,
        project_id: row.projectId,
        asset_id: row.assetId,
        live_stream_id: row.liveStreamId,
        policy: row.policy,
        token_required: row.tokenRequired,
        created_at: row.createdAt.toISOString(),
      })),
    });
  });

  app.post<{ Params: { id: string } }>("/admin/playback/:id/policy", { preHandler }, async (req, reply) => {
    if (!deps.playbackIds) {
      await reply.code(501).send({ error: "playback_repo_unavailable" });
      return;
    }
    const parsed = PlaybackPolicyBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.issues });
      return;
    }
    const playback = await deps.playbackIds.byId(req.params.id);
    if (!playback) {
      await reply.code(404).send({ error: "playback_not_found" });
      return;
    }
    await deps.playbackIds.updatePolicy(playback.id, {
      policy: parsed.data.policy,
      tokenRequired: parsed.data.token_required,
    });
    await reply.code(200).send({
      id: playback.id,
      project_id: playback.projectId,
      asset_id: playback.assetId,
      live_stream_id: playback.liveStreamId,
      policy: parsed.data.policy,
      token_required: parsed.data.token_required,
    });
  });

  app.get("/admin/projects", { preHandler }, async (req, reply) => {
    const query = req.query as Record<string, string | undefined>;
    const customerId = query["customer_id"];
    const rows = await deps.videoDb
      .select()
      .from(projects)
      .where(customerId ? eq(projects.customerId, customerId) : undefined)
      .orderBy(desc(projects.createdAt))
      .limit(200);
    const usageByProjectId = new Map(
      await Promise.all(rows.map(async (row) => [row.id, await summarizeProjectUsage(deps.videoDb, row.id)] as const)),
    );
    await reply.code(200).send({
      items: rows.map((row) => ({
        id: row.id,
        customer_id: row.customerId,
        name: row.name,
        created_at: row.createdAt.toISOString(),
        usage: {
          assets: usageByProjectId.get(row.id)?.assets ?? 0,
          uploads: usageByProjectId.get(row.id)?.uploads ?? 0,
          live_streams: usageByProjectId.get(row.id)?.liveStreams ?? 0,
          webhooks: usageByProjectId.get(row.id)?.webhooks ?? 0,
        },
      })),
    });
  });

  app.post<{ Params: { id: string } }>("/admin/live-streams/:id/end", { preHandler }, async (req, reply) => {
    const stream = await deps.liveStreamsRepo.byId(req.params.id);
    if (!stream) {
      await reply.code(404).send({ error: "not_found" });
      return;
    }
    if (stream.endedAt) {
      await reply.code(200).send({
        ok: true,
        recording_asset_id: stream.recordingAssetId ?? null,
        recording_execution_id: null,
      });
      return;
    }
    const endedAt = new Date();
    await deps.liveStreamsRepo.updateStatus(req.params.id, "ended", {
      endedAt,
      lastSeenAt: endedAt,
    });
    if (deps.usageLedger) {
      const durationSec = Math.max(0, Math.ceil((endedAt.getTime() - stream.createdAt.getTime()) / 1000));
      await deps.usageLedger.recordLiveUsage({
        projectId: stream.projectId,
        liveStreamId: stream.id,
        durationSec,
      });
    }
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
    const query = _req.query as Record<string, string | undefined>;
    const projectId = query["project_id"];
    let scopedStreamIds: string[] | null = null;
    if (projectId) {
      scopedStreamIds = (
        await deps.videoDb
          .select({ id: liveStreams.id })
          .from(liveStreams)
          .where(eq(liveStreams.projectId, projectId))
      ).map((row) => row.id);
      if (scopedStreamIds.length === 0) {
        await reply.code(200).send({ items: [] });
        return;
      }
    }
    const rows = await deps.videoDb
      .select()
      .from(recordings)
      .where(scopedStreamIds ? inArray(recordings.liveStreamId, scopedStreamIds) : undefined)
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

  app.post<{ Params: { id: string } }>("/admin/recordings/:id/retry", { preHandler }, async (req, reply) => {
    if (!deps.execution) {
      await reply.code(501).send({ error: "execution_unavailable" });
      return;
    }
    const recording = await deps.recordingsRepo.byId(req.params.id);
    if (!recording) {
      await reply.code(404).send({ error: "recording_not_found" });
      return;
    }
    if (!recording.assetId) {
      await reply.code(409).send({ error: "recording_asset_missing" });
      return;
    }
    const asset = await deps.assets.byId(recording.assetId);
    if (!asset || asset.deletedAt) {
      await reply.code(404).send({ error: "asset_not_found" });
      return;
    }
    if (asset.status === "ready") {
      await reply.code(409).send({ error: "asset_already_ready" });
      return;
    }
    try {
      const out = await deps.execution.retryAsset(asset.id);
      await deps.recordingsRepo.updateStatus(recording.id, "pending", {
        assetId: asset.id,
      });
      await reply.code(202).send({
        recording_id: recording.id,
        asset_id: asset.id,
        status: "pending",
        execution_id: out.executionId,
      });
    } catch (err) {
      const message = err instanceof Error ? err.message : "retry_failed";
      await reply.code(502).send({ error: "retry_failed", message });
    }
  });

  app.get("/admin/webhook-failures", { preHandler }, async (req, reply) => {
    const query = req.query as Record<string, string | undefined>;
    const limit = Math.min(parseInt(query["limit"] ?? "50", 10) || 50, 200);
    const endpointId = query["endpoint_id"];
    const projectId = query["project_id"];
    if (projectId && !endpointId) {
      const endpointRows = await deps.videoDb
        .select({ id: webhookEndpoints.id })
        .from(webhookEndpoints)
        .where(eq(webhookEndpoints.projectId, projectId));
      const endpointIds = endpointRows.map((row) => row.id);
      if (endpointIds.length === 0) {
        await reply.code(200).send({ items: [] });
        return;
      }
      const failures = (
        await Promise.all(endpointIds.map((id) => deps.failures.list({ endpointId: id, limit })))
      )
        .flat()
        .sort((a, b) => b.deadLetteredAt.getTime() - a.deadLetteredAt.getTime())
        .slice(0, limit);
      await reply.code(200).send({
        items: failures.map((row) => ({
          ...row,
          projectId,
        })),
      });
      return;
    }
    const list = await deps.failures.list(
      endpointId ? { endpointId, limit } : { limit },
    );
    let projectIdByEndpoint = new Map<string, string>();
    if (list.length > 0) {
      const endpointRows = await deps.videoDb
        .select({ id: webhookEndpoints.id, projectId: webhookEndpoints.projectId })
        .from(webhookEndpoints)
        .where(inArray(webhookEndpoints.id, [...new Set(list.map((row) => row.endpointId))]));
      projectIdByEndpoint = new Map(endpointRows.map((row) => [row.id, row.projectId]));
    }
    await reply.code(200).send({
      items: list.map((row) => ({
        ...row,
        projectId: projectIdByEndpoint.get(row.endpointId) ?? null,
      })),
    });
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

function liveSessionForRow(
  directory: LiveSessionDirectory,
  row: { id: string; sessionId: string | null },
) {
  return row.sessionId ? directory.get(row.sessionId) ?? directory.getByStreamId(row.id) : directory.getByStreamId(row.id);
}

function adminStreamHealth(
  row: { status: string; endedAt: Date | null; lastSeenAt: Date | null },
  sessionKnown: boolean,
  staleAfterSec: number,
): "healthy" | "degraded" | "stale" | "ended" {
  if (row.endedAt) return "ended";
  if (row.status === "reconnecting") return "degraded";
  if (!sessionKnown) return "degraded";
  if (!row.lastSeenAt) return "degraded";
  const idleSec = Math.max(0, Math.floor((Date.now() - row.lastSeenAt.getTime()) / 1000));
  if (idleSec > staleAfterSec) return "stale";
  return "healthy";
}

function serializeCharge(
  workId: string | null,
  charges: Map<string, ChargeSummary>,
): Record<string, unknown> | null {
  if (!workId) return null;
  const charge = charges.get(workId);
  if (!charge) return null;
  return {
    work_id: charge.workId,
    reservation_id: charge.reservationId,
    customer_id: charge.customerId,
    kind: charge.kind,
    state: charge.state,
    estimated_amount_cents: charge.estimatedAmountCents,
    committed_amount_cents: charge.committedAmountCents,
    refunded_amount_cents: charge.refundedAmountCents,
    capability: charge.capability,
    model: charge.model,
    created_at: charge.createdAt,
    resolved_at: charge.resolvedAt,
  };
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
