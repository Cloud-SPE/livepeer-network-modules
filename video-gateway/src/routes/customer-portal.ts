import { desc, eq, inArray } from "drizzle-orm";
import type { FastifyInstance, FastifyReply, FastifyRequest, preHandlerAsyncHookHandler } from "fastify";
import type { CustomerPortal } from "@livepeer-rewrite/customer-portal";
import { z } from "zod";

import type { Config } from "../config.js";
import type { Db as VideoDb } from "../db/pool.js";
import { assets, liveStreams, recordings, uploads } from "../db/schema.js";
import { defaultPricingConfig } from "../engine/config/pricing.js";
import type { AssetRepo, LiveStreamRepo } from "../engine/index.js";
import type { LiveSessionDirectory } from "../livepeer/liveSessionDirectory.js";
import type { VideoRouteSelector } from "../livepeer/routeSelector.js";
import type { RecordingRepo } from "../repo/index.js";
import type { UsageRecordRepo } from "../repo/usage.js";
import { customerProjectIds, ensureDefaultProject, listProjectsForCustomer } from "../service/projects.js";
import type { PlaybackIdRepo } from "../repo/playbackIds.js";
import type { MutableEncodingJobRepo } from "../repo/encodingJobs.js";
import type { MutableRenditionRepo } from "../repo/renditions.js";
import type { AbrExecutionManager } from "../service/abrExecution.js";
import { usageWorkId, type ChargeSummary, type UsageLedger } from "../service/usageLedger.js";
import { maybeHandoffRecording, provisionLiveStream } from "./live-streams.js";
import { createProject, deleteProject, getProjectById, renameProject, summarizeProjectUsage } from "../service/projects.js";

declare module "fastify" {
  interface FastifyRequest {
    customerSession?: Awaited<ReturnType<CustomerPortal["customerTokenService"]["authenticate"]>>;
  }
}

const CreatePortalStreamBody = z.object({
  name: z.string().trim().min(1).max(160),
});

const UpdateRecordPolicyBody = z.object({
  record_to_vod: z.boolean(),
});

const CreatePortalProjectBody = z.object({
  name: z.string().trim().min(1).max(120),
});

const UpdatePortalProjectBody = z.object({
  name: z.string().trim().min(1).max(120),
});

const CreatePortalUploadBody = z.object({
  filename: z.string().trim().min(1).max(255),
  size: z.number().int().positive(),
  contentType: z.string().trim().min(1).max(255).optional(),
});

export interface RegisterVideoCustomerPortalRoutesDeps {
  portal: CustomerPortal;
  cfg: Config;
  routeSelector: VideoRouteSelector;
  liveSessions: LiveSessionDirectory;
  videoDb?: VideoDb;
  assetsRepo?: AssetRepo;
  liveStreamsRepo?: LiveStreamRepo;
  recordingsRepo?: RecordingRepo;
  playbackIds?: PlaybackIdRepo;
  jobsRepo?: MutableEncodingJobRepo;
  renditionsRepo?: MutableRenditionRepo;
  execution?: AbrExecutionManager;
  usageRecords?: UsageRecordRepo;
  usageLedger?: UsageLedger;
}

export function registerVideoCustomerPortalRoutes(
  app: FastifyInstance,
  deps: RegisterVideoCustomerPortalRoutesDeps,
): void {
  const requireCustomer = customerAuthPreHandler(deps.portal.customerTokenService);

  app.get("/portal/pricing", { preHandler: requireCustomer }, async (_req, reply) => {
    const pricing = defaultPricingConfig();
    await reply.code(200).send({
      vod_pipeline_policy: {
        baseline: {
          capability: "video:transcode.abr",
          pipeline: "abr_ladder",
          description: "Baseline jobs route to the ABR runner with a one-rendition ladder when appropriate.",
        },
        standard: {
          capability: "video:transcode.abr",
          pipeline: "abr_ladder",
          description: "Standard jobs route to the ABR ladder runner for multi-rendition packaging.",
        },
        premium: {
          capability: "video:transcode.abr",
          pipeline: "abr_ladder",
          description: "Premium jobs route to the ABR ladder runner with the richest codec ladder.",
        },
      },
      live: {
        billing_unit: "stream_seconds",
        cents_per_second: pricing.liveCentsPerSecond,
        cents_per_minute: Number((pricing.liveCentsPerSecond * 60).toFixed(6)),
      },
      vod: {
        billing_unit: "rendition_seconds",
        overhead_cents: pricing.overheadCents,
        cents_per_second: pricing.vodCentsPerSecond,
      },
    });
  });

  app.get("/portal/usage", { preHandler: requireCustomer }, async (req, reply) => {
    if (!deps.videoDb || !deps.usageRecords) {
      await reply.code(501).send({ error: "usage_unavailable" });
      return;
    }
    const projectIds = [...(await customerProjectIds(deps.videoDb, req.customerSession!.customer.id))];
    const rows = await deps.usageRecords.listByProjects(projectIds, 100);
    const chargeByWorkId = deps.usageLedger
      ? await deps.usageLedger.listChargesByWorkIds(
          rows
            .map((row) => usageWorkId(row))
            .filter((workId): workId is string => workId !== null),
        )
      : new Map();
    const summary = deps.usageLedger
      ? await deps.usageLedger.summarizeCustomer(req.customerSession!.customer.id)
      : {
          topupTotalCents: 0,
          usageCommittedCents: rows.reduce((sum, row) => sum + row.amountCents, 0),
          reservedOpenCents: 0,
          refundedCents: 0,
        };
    await reply.code(200).send({
      items: rows.map((row) => ({
        id: row.id,
        capability: row.capability,
        amount_cents: row.amountCents,
        created_at: row.createdAt.toISOString(),
        asset_id: row.assetId,
        live_stream_id: row.liveStreamId,
        work_id: usageWorkId(row),
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

  app.get("/portal/projects", { preHandler: requireCustomer }, async (req, reply) => {
    if (!deps.videoDb) {
      await reply.code(501).send({ error: "video_db_unavailable" });
      return;
    }
    const defaultProject = await ensureDefaultProject(deps.videoDb, req.customerSession!.customer.id);
    const rows = await listProjectsForCustomer(deps.videoDb, req.customerSession!.customer.id);
    const usageById = new Map(
      await Promise.all(
        rows.map(async (row) => [row.id, await summarizeProjectUsage(deps.videoDb!, row.id)] as const),
      ),
    );
    await reply.code(200).send({
      items: rows.map((row) => ({
        id: row.id,
        name: row.name,
        createdAt: row.createdAt.toISOString(),
        isDefault: row.id === defaultProject.id,
        usage: {
          assets: usageById.get(row.id)?.assets ?? 0,
          uploads: usageById.get(row.id)?.uploads ?? 0,
          live_streams: usageById.get(row.id)?.liveStreams ?? 0,
          webhooks: usageById.get(row.id)?.webhooks ?? 0,
        },
      })),
      defaultProjectId: defaultProject.id,
    });
  });

  app.post("/portal/projects", { preHandler: requireCustomer }, async (req, reply) => {
    const parsed = CreatePortalProjectBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.issues });
      return;
    }
    if (!deps.videoDb) {
      await reply.code(501).send({ error: "video_db_unavailable" });
      return;
    }
    const project = await createProject(deps.videoDb, {
      customerId: req.customerSession!.customer.id,
      name: parsed.data.name,
    });
    const usage = await summarizeProjectUsage(deps.videoDb, project.id);
    await reply.code(201).send({
      id: project.id,
      name: project.name,
      createdAt: project.createdAt.toISOString(),
      usage: {
        assets: usage.assets,
        uploads: usage.uploads,
        live_streams: usage.liveStreams,
        webhooks: usage.webhooks,
      },
    });
  });

  app.patch<{ Params: { id: string } }>("/portal/projects/:id", { preHandler: requireCustomer }, async (req, reply) => {
    const parsed = UpdatePortalProjectBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.issues });
      return;
    }
    if (!deps.videoDb) {
      await reply.code(501).send({ error: "video_db_unavailable" });
      return;
    }
    const project = await getProjectById(deps.videoDb, req.params.id);
    if (!project || project.customerId !== req.customerSession!.customer.id) {
      await reply.code(404).send({ error: "project_not_found" });
      return;
    }
    const renamed = await renameProject(deps.videoDb, {
      projectId: project.id,
      name: parsed.data.name,
    });
    if (!renamed) {
      await reply.code(404).send({ error: "project_not_found" });
      return;
    }
    const usage = await summarizeProjectUsage(deps.videoDb, renamed.id);
    await reply.code(200).send({
      id: renamed.id,
      name: renamed.name,
      createdAt: renamed.createdAt.toISOString(),
      usage: {
        assets: usage.assets,
        uploads: usage.uploads,
        live_streams: usage.liveStreams,
        webhooks: usage.webhooks,
      },
    });
  });

  app.delete<{ Params: { id: string } }>("/portal/projects/:id", { preHandler: requireCustomer }, async (req, reply) => {
    if (!deps.videoDb) {
      await reply.code(501).send({ error: "video_db_unavailable" });
      return;
    }
    const customerId = req.customerSession!.customer.id;
    const project = await getProjectById(deps.videoDb, req.params.id);
    if (!project || project.customerId !== customerId) {
      await reply.code(404).send({ error: "project_not_found" });
      return;
    }
    const projectsForCustomer = await listProjectsForCustomer(deps.videoDb, customerId);
    if (projectsForCustomer.length <= 1) {
      await reply.code(409).send({ error: "last_project_forbidden" });
      return;
    }
    const usage = await summarizeProjectUsage(deps.videoDb, project.id);
    if (usage.assets > 0 || usage.uploads > 0 || usage.liveStreams > 0 || usage.webhooks > 0) {
      await reply.code(409).send({
        error: "project_not_empty",
        usage: {
          assets: usage.assets,
          uploads: usage.uploads,
          live_streams: usage.liveStreams,
          webhooks: usage.webhooks,
        },
      });
      return;
    }
    await deleteProject(deps.videoDb, project.id);
    await reply.code(204).send();
  });

  app.get("/portal/assets", { preHandler: requireCustomer }, async (req, reply) => {
    if (!deps.videoDb) {
      await reply.code(501).send({ error: "video_db_unavailable" });
      return;
    }
    const projectIds = await customerProjectIds(deps.videoDb, req.customerSession!.customer.id);
    const items = projectIds.size === 0
      ? []
      : await deps.videoDb
          .select()
          .from(assets)
          .where(inArray(assets.projectId, [...projectIds]))
          .orderBy(desc(assets.createdAt));
    const playbackByAssetId = new Map<string, string>();
    if (deps.playbackIds) {
      for (const row of items) {
        const [playback] = await deps.playbackIds.byAsset(row.id);
        if (playback) playbackByAssetId.set(row.id, playback.id);
      }
    }
    await reply.code(200).send({
      items: items.map((row) => ({
        id: row.id,
        status: row.status,
        projectId: row.projectId,
        durationSec: row.durationSec !== null ? Number(row.durationSec) : null,
        createdAt: row.createdAt.toISOString(),
        deletedAt: row.deletedAt?.toISOString() ?? null,
        playbackId: playbackByAssetId.get(row.id) ?? null,
        playbackUrl: playbackByAssetId.has(row.id)
          ? `/v1/playback/${encodeURIComponent(playbackByAssetId.get(row.id)!)}`
          : null,
      })),
    });
  });

  app.post("/portal/uploads", { preHandler: requireCustomer }, async (req, reply) => {
    const parsed = CreatePortalUploadBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_request", details: parsed.error.flatten() });
      return;
    }
    if (!deps.videoDb || !deps.assetsRepo) {
      await reply.code(501).send({ error: "upload_storage_unavailable" });
      return;
    }
    const project = await ensureDefaultProject(deps.videoDb, req.customerSession!.customer.id);
    const assetId = `asset_${randomHex16()}`;
    const uploadId = `upload_${randomHex16()}`;
    const storageKey = `uploads/${project.id}/${assetId}/${sanitizeFilename(parsed.data.filename)}`;
    const now = new Date();
    const expiresAt = new Date(now.getTime() + 24 * 60 * 60 * 1000);

    await deps.assetsRepo.insert({
      id: assetId,
      projectId: project.id,
      status: "preparing",
      sourceType: "upload",
      sourceUrl: storageKey,
      encodingTier: "baseline",
      createdAt: now,
    });
    await deps.videoDb.insert(uploads).values({
      id: uploadId,
      projectId: project.id,
      assetId,
      status: "created",
      uploadUrl: `${deps.cfg.vodTusPath}/${uploadId}`,
      storageKey,
      expiresAt,
      createdAt: now,
    });

    await reply.code(201).send({
      assetId,
      uploadId,
      projectId: project.id,
      uploadUrl: `${deps.cfg.vodTusPath}/${uploadId}`,
    });
  });

  app.delete<{ Params: { id: string } }>("/portal/assets/:id", { preHandler: requireCustomer }, async (req, reply) => {
    if (!deps.assetsRepo || !deps.videoDb) {
      await reply.code(501).send({ error: "asset_repo_unavailable" });
      return;
    }
    const asset = await deps.assetsRepo.byId(req.params.id);
    const projectIds = await customerProjectIds(deps.videoDb, req.customerSession!.customer.id);
    if (!asset || !projectIds.has(asset.projectId)) {
      await reply.code(404).send({ error: "not_found" });
      return;
    }
    await deps.assetsRepo.softDelete(asset.id, new Date());
    await reply.code(204).send();
  });

  app.post<{ Params: { id: string } }>("/portal/assets/:id/restore", { preHandler: requireCustomer }, async (req, reply) => {
    if (!deps.videoDb) {
      await reply.code(501).send({ error: "video_db_unavailable" });
      return;
    }
    const rows = await deps.videoDb
      .select()
      .from(assets)
      .where(eq(assets.id, req.params.id))
      .limit(1);
    const asset = rows[0];
    const projectIds = await customerProjectIds(deps.videoDb, req.customerSession!.customer.id);
    if (!asset || !projectIds.has(asset.projectId)) {
      await reply.code(404).send({ error: "not_found" });
      return;
    }
    await deps.videoDb
      .update(assets)
      .set({ deletedAt: null, status: restoredAssetStatus(asset) })
      .where(eq(assets.id, req.params.id));
    await reply.code(204).send();
  });

  app.get("/portal/live-streams", { preHandler: requireCustomer }, async (req, reply) => {
    if (!deps.videoDb) {
      await reply.code(501).send({ error: "video_db_unavailable" });
      return;
    }
    const projectIds = await customerProjectIds(deps.videoDb, req.customerSession!.customer.id);
    if (projectIds.size === 0) {
      await reply.code(200).send({ items: [] });
      return;
    }
    const rows = await deps.videoDb
      .select()
      .from(liveStreams)
      .where(inArray(liveStreams.projectId, [...projectIds]))
      .orderBy(desc(liveStreams.createdAt));
    await reply.code(200).send({
      items: rows.map((row) => {
        const session =
          row.sessionId !== null
            ? deps.liveSessions.get(row.sessionId)
            : deps.liveSessions.getByStreamId(row.id);
        return {
          id: row.id,
          name: row.name ?? row.id,
          status: portalStatus(row.status),
          rtmpIngestUrl: session?.brokerRtmpUrl ?? "",
          playbackUrl: session?.hlsPlaybackUrl ?? "",
          viewerCount: null,
          recordToVod: row.recordingEnabled,
          createdAt: row.createdAt.toISOString(),
          endedAt: row.endedAt?.toISOString() ?? null,
        };
      }),
    });
  });

  app.post("/portal/live-streams", { preHandler: requireCustomer }, async (req, reply) => {
    const parsed = CreatePortalStreamBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_request", details: parsed.error.flatten() });
      return;
    }
    if (!deps.liveStreamsRepo) {
      await reply.code(501).send({ error: "live_stream_repo_unavailable" });
      return;
    }
    const session = req.customerSession!;
    if (!deps.videoDb) {
      await reply.code(501).send({ error: "video_db_unavailable" });
      return;
    }
    const project = await ensureDefaultProject(deps.videoDb, session.customer.id);
    const out = await provisionLiveStream(
      {
        cfg: deps.cfg,
        routeSelector: deps.routeSelector,
        liveSessions: deps.liveSessions,
        liveStreamsRepo: deps.liveStreamsRepo,
      },
      {
        projectId: project.id,
        name: parsed.data.name,
        customerTier: normalizeCustomerTier(session.customer.tier),
        recordToVod: false,
        requestHeaders: req.headers,
      },
    );
    await reply.code(201).send({
      id: out.streamId,
      projectId: project.id,
      name: out.name,
      status: "live",
      rtmpIngestUrl: out.rtmpPushUrl,
      playbackUrl: out.hlsPlaybackUrl,
      viewerCount: null,
      createdAt: out.createdAt.toISOString(),
      endedAt: null,
      sessionKey: out.streamKey,
    });
  });

  app.post<{ Params: { id: string } }>("/portal/live-streams/:id/end", { preHandler: requireCustomer }, async (req, reply) => {
    if (!deps.liveStreamsRepo || !deps.videoDb) {
      await reply.code(501).send({ error: "live_stream_repo_unavailable" });
      return;
    }
    const stream = await deps.liveStreamsRepo.byId(req.params.id);
    const projectIds = await customerProjectIds(deps.videoDb, req.customerSession!.customer.id);
    if (!stream || !projectIds.has(stream.projectId)) {
      await reply.code(404).send({ error: "not_found" });
      return;
    }
    if (stream.endedAt) {
      await reply.code(200).send({
        ok: true,
        recordingAssetId: stream.recordingAssetId ?? null,
        recordingExecutionId: null,
      });
      return;
    }
    const endedAt = new Date();
    await deps.liveStreamsRepo.updateStatus(stream.id, "ended", {
      lastSeenAt: endedAt,
      endedAt,
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
      recordingAssetId: handoff?.assetId ?? stream.recordingAssetId ?? null,
      recordingExecutionId: handoff?.executionId ?? null,
    });
  });

  app.post<{ Params: { id: string } }>("/portal/live-streams/:id/record", { preHandler: requireCustomer }, async (req, reply) => {
    const parsed = UpdateRecordPolicyBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_request", details: parsed.error.flatten() });
      return;
    }
    if (!deps.liveStreamsRepo || !deps.videoDb) {
      await reply.code(501).send({ error: "live_stream_repo_unavailable" });
      return;
    }
    const stream = await deps.liveStreamsRepo.byId(req.params.id);
    const projectIds = await customerProjectIds(deps.videoDb, req.customerSession!.customer.id);
    if (!stream || !projectIds.has(stream.projectId)) {
      await reply.code(404).send({ error: "not_found" });
      return;
    }
    await deps.liveStreamsRepo.updateStatus(stream.id, stream.status, {
      recordingEnabled: parsed.data.record_to_vod,
      lastSeenAt: new Date(),
    });
    if (
      parsed.data.record_to_vod &&
      deps.recordingsRepo &&
      (stream.status === "active" || stream.status === "reconnecting")
    ) {
      const existing = await deps.recordingsRepo.byLiveStream(stream.id);
      const hasOpen = existing.some((row) => row.endedAt === null);
      if (!hasOpen) {
        await deps.recordingsRepo.insert({
          id: `rec_${randomHex16()}`,
          liveStreamId: stream.id,
          assetId: null,
          status: "running",
          startedAt: new Date(),
          endedAt: null,
        });
      }
    }
    await reply.code(200).send({ ok: true });
  });

  app.get("/portal/recordings", { preHandler: requireCustomer }, async (req, reply) => {
    if (!deps.videoDb) {
      await reply.code(501).send({ error: "video_db_unavailable" });
      return;
    }
    const projectIds = await customerProjectIds(deps.videoDb, req.customerSession!.customer.id);
    if (projectIds.size === 0) {
      await reply.code(200).send({ items: [] });
      return;
    }
    const streamRows = await deps.videoDb
      .select({
        id: liveStreams.id,
        name: liveStreams.name,
      })
      .from(liveStreams)
      .where(inArray(liveStreams.projectId, [...projectIds]));
    if (streamRows.length === 0) {
      await reply.code(200).send({ items: [] });
      return;
    }
    const streamNameById = new Map(streamRows.map((row) => [row.id, row.name ?? row.id]));
    const rows = await deps.videoDb
      .select()
      .from(recordings)
      .where(inArray(recordings.liveStreamId, streamRows.map((row) => row.id)))
      .orderBy(desc(recordings.createdAt));
    await reply.code(200).send({
      items: rows.map((row) => ({
        id: row.id,
        streamId: row.liveStreamId,
        streamName: streamNameById.get(row.liveStreamId) ?? row.liveStreamId,
        assetId: row.assetId,
        status: row.status,
        assetUrl: row.assetId ? `/v1/videos/assets/${encodeURIComponent(row.assetId)}` : null,
        durationSec:
          row.startedAt && row.endedAt
            ? Math.max(0, (row.endedAt.getTime() - row.startedAt.getTime()) / 1000)
            : null,
        startedAt: (row.startedAt ?? row.createdAt).toISOString(),
        endedAt: row.endedAt?.toISOString() ?? null,
      })),
    });
  });
}

function customerAuthPreHandler(
  service: CustomerPortal["customerTokenService"],
): preHandlerAsyncHookHandler {
  return async (req: FastifyRequest, reply: FastifyReply): Promise<void> => {
    try {
      req.customerSession = await service.authenticate(req.headers.authorization);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      await reply.code(401).send({ error: "authentication_failed", message });
    }
  };
}

function restoredAssetStatus(asset: { readyAt?: Date | null; selectedOffering?: string | null }): "ready" | "queued" | "preparing" {
  if (asset.readyAt) return "ready";
  if (asset.selectedOffering) return "queued";
  return "preparing";
}

function normalizeCustomerTier(tier: string): "free" | "prepaid" | "enterprise" {
  if (tier === "prepaid" || tier === "enterprise") {
    return tier;
  }
  return "free";
}

function portalStatus(status: string): string {
  if (status === "active" || status === "reconnecting") return "live";
  return status;
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

function randomHex16(): string {
  return Math.random().toString(16).slice(2, 18).padEnd(16, "0");
}

function sanitizeFilename(value: string): string {
  return value.replace(/[^A-Za-z0-9._-]+/g, "_");
}
