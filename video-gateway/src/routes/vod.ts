import type { FastifyInstance } from "fastify";
import { z } from "zod";
import type { AssetRepo } from "../engine/index.js";
import type { VideoRouteSelector } from "../livepeer/routeSelector.js";
import { buildVodSelectionHints } from "../livepeer/selectionPolicy.js";
import { defaultPricingConfig } from "../engine/config/pricing.js";
import { estimateCost } from "../engine/service/costQuoter.js";
import { expandTier } from "../engine/config/encodingLadder.js";
import type { PlaybackIdRepo } from "../repo/playbackIds.js";
import type { MutableRenditionRepo } from "../repo/renditions.js";
import type { MutableEncodingJobRepo } from "../repo/encodingJobs.js";
import type { AbrExecutionManager } from "../service/abrExecution.js";
import type { ChargeSummary, UsageLedger } from "../service/usageLedger.js";

const VodSubmitBody = z.object({
  asset_id: z.string().min(1),
  encoding_tier: z.enum(["baseline", "standard", "premium"]).default("baseline"),
  estimated_duration_sec: z.number().positive().optional(),
  offering: z.string().optional(),
});

const VodQuoteBody = z.object({
  encoding_tier: z.enum(["baseline", "standard", "premium"]).default("baseline"),
  estimated_duration_sec: z.number().positive(),
});

const ListAssetsQuery = z.object({
  include_deleted: z.coerce.boolean().optional().default(false),
});

export interface VodDeps {
  routeSelector: VideoRouteSelector;
  assetsRepo?: AssetRepo;
  renditionsRepo?: MutableRenditionRepo;
  jobsRepo?: MutableEncodingJobRepo;
  playbackIds?: PlaybackIdRepo;
  execution?: AbrExecutionManager;
  usageLedger?: UsageLedger;
}

export function registerVod(app: FastifyInstance, deps: VodDeps): void {
  app.post("/v1/vod/quote", async (req, reply) => {
    const parsed = VodQuoteBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.issues });
      return;
    }
    const renditions = expandTier(parsed.data.encoding_tier);
    const quote = estimateCost({
      capability: "video:transcode.abr",
      callerTier: "customer",
      renditions,
      estimatedSeconds: parsed.data.estimated_duration_sec,
      pricing: defaultPricingConfig(),
    });
    await reply.code(200).send({
      capability: quote.capability,
      pipeline: "abr_ladder",
      encoding_tier: parsed.data.encoding_tier,
      estimated_duration_sec: parsed.data.estimated_duration_sec,
      estimated_cost_usd_cents: quote.cents,
      renditions: quote.renditions.map((row) => ({
        resolution: row.resolution,
        codec: row.codec,
        bitrate_kbps: row.bitrateKbps,
      })),
    });
  });

  app.post("/v1/vod/submit", async (req, reply) => {
    const parsed = VodSubmitBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.issues });
      return;
    }
    if (!deps.assetsRepo) {
      await reply.code(501).send({ error: "asset_repo_unavailable" });
      return;
    }
    const asset = await deps.assetsRepo.byId(parsed.data.asset_id);
    if (!asset || asset.deletedAt) {
      await reply.code(404).send({ error: "asset_not_found" });
      return;
    }
    const selectionHints = buildVodSelectionHints({
      encodingTier: parsed.data.encoding_tier,
    });
    const [route] = await deps.routeSelector.select({
      capability: "video:transcode.abr",
      offering: parsed.data.offering ?? "default",
      headers: req.headers,
      preferredExtra: selectionHints.preferredExtra,
      supportFilter: selectionHints.supportFilter,
    });
    if (!route) {
      await reply.code(503).send({
        error: "no_video_transcode_route",
        message: "no video:transcode.abr route supports the requested VOD job",
      });
      return;
    }
    let charge: ChargeSummary | null = null;
    if (deps.usageLedger) {
      try {
        charge = await deps.usageLedger.reserveVodEstimate({
          projectId: asset.projectId,
          assetId: parsed.data.asset_id,
          encodingTier: parsed.data.encoding_tier,
          estimatedDurationSec: parsed.data.estimated_duration_sec ?? asset.durationSec ?? null,
        });
      } catch (err) {
        const message = err instanceof Error ? err.message : "billing_reserve_failed";
        await reply.code(402).send({
          error: "billing_reserve_failed",
          message,
        });
        return;
      }
    }
    await deps.assetsRepo.updateStatus(parsed.data.asset_id, "queued", {
      encodingTier: parsed.data.encoding_tier,
      selectedOffering: route.offering,
      errorMessage: undefined,
    });
    let execution: { executionId: string } | null = null;
    if (deps.execution) {
      try {
        execution = await deps.execution.submitAsset({
          assetId: parsed.data.asset_id,
          route,
        });
      } catch (err) {
        if (deps.usageLedger) {
          await deps.usageLedger.refundVodUsage({
            projectId: asset.projectId,
            assetId: parsed.data.asset_id,
          });
        }
        const message = err instanceof Error ? err.message : "execution_start_failed";
        await reply.code(502).send({
          error: "execution_start_failed",
          message,
        });
        return;
      }
    }
    await reply.code(202).send({
      asset_id: parsed.data.asset_id,
      project_id: asset.projectId,
      status: "queued",
      selected_capability: "video:transcode.abr",
      selected_pipeline: "abr_ladder",
      encoding_tier: parsed.data.encoding_tier,
      selected_offering: route.offering,
      selected_broker_url: route.brokerUrl,
      execution_id: execution?.executionId ?? null,
      billing: serializeCharge(charge),
    });
  });

  app.get("/v1/vod/:asset_id", async (req, reply) => {
    if (!deps.assetsRepo) {
      await reply.code(501).send({ error: "asset_repo_unavailable" });
      return;
    }
    const { asset_id } = req.params as { asset_id: string };
    const asset = await deps.assetsRepo.byId(asset_id);
    if (!asset) {
      await reply.code(404).send({ error: "asset_not_found" });
      return;
    }
    const [playback] = deps.playbackIds ? await deps.playbackIds.byAsset(asset.id) : [];
    const renditions = deps.renditionsRepo ? await deps.renditionsRepo.byAsset(asset.id) : [];
    const jobs = deps.jobsRepo ? await deps.jobsRepo.byAsset(asset.id) : [];
    const charge = deps.usageLedger ? await deps.usageLedger.getChargeByAsset(asset.id) : null;
    await reply.code(200).send({
      asset_id: asset.id,
      project_id: asset.projectId,
      status: asset.status,
      encoding_tier: asset.encodingTier,
      source_type: asset.sourceType,
      duration_sec: asset.durationSec ?? null,
      selected_offering: asset.selectedOffering ?? null,
      ready_at: asset.readyAt?.toISOString() ?? null,
      error_message: asset.errorMessage ?? null,
      playback_id: playback?.id ?? null,
      playback_url: playback ? `/v1/playback/${encodeURIComponent(playback.id)}` : null,
      billing: serializeCharge(charge),
      renditions: renditions.map((row) => ({
        id: row.id,
        resolution: row.resolution,
        codec: row.codec,
        bitrate_kbps: row.bitrateKbps,
        storage_key: row.storageKey ?? null,
        status: row.status,
        duration_sec: row.durationSec ?? null,
        completed_at: row.completedAt?.toISOString() ?? null,
      })),
      jobs: jobs.map((row) => ({
        id: row.id,
        kind: row.kind,
        status: row.status,
        worker_url: row.workerUrl ?? null,
        error_message: row.errorMessage ?? null,
        started_at: row.startedAt?.toISOString() ?? null,
        completed_at: row.completedAt?.toISOString() ?? null,
      })),
    });
  });

  app.get("/v1/videos/assets", async (req, reply) => {
    if (!deps.assetsRepo) {
      await reply.code(501).send({ error: "asset_repo_unavailable" });
      return;
    }
    const parsed = ListAssetsQuery.safeParse(req.query);
    const includeDeleted = parsed.success ? parsed.data.include_deleted : false;
    const items = await deps.assetsRepo.recent({ limit: 100 });
    await reply.code(200).send({
      items: items
        .filter((row) => (includeDeleted ? true : !row.deletedAt))
        .map((row) => ({
          id: row.id,
          project_id: row.projectId,
          status: row.status,
          source_type: row.sourceType,
          encoding_tier: row.encodingTier,
          duration_sec: row.durationSec ?? null,
          created_at: row.createdAt.toISOString(),
          ready_at: row.readyAt?.toISOString() ?? null,
          deleted_at: row.deletedAt?.toISOString() ?? null,
        })),
      include_deleted: includeDeleted,
    });
  });

  app.get("/v1/videos/assets/:id", async (req, reply) => {
    if (!deps.assetsRepo) {
      await reply.code(501).send({ error: "asset_repo_unavailable" });
      return;
    }
    const { id } = req.params as { id: string };
    const parsed = ListAssetsQuery.safeParse(req.query);
    const includeDeleted = parsed.success ? parsed.data.include_deleted : false;
    const asset = await deps.assetsRepo.byId(id);
    if (!asset || (!includeDeleted && asset.deletedAt)) {
      await reply.code(404).send({ error: "asset_not_found" });
      return;
    }
    const [playback] = deps.playbackIds ? await deps.playbackIds.byAsset(asset.id) : [];
    const renditions = deps.renditionsRepo ? await deps.renditionsRepo.byAsset(asset.id) : [];
    const jobs = deps.jobsRepo ? await deps.jobsRepo.byAsset(asset.id) : [];
    const charge = deps.usageLedger ? await deps.usageLedger.getChargeByAsset(asset.id) : null;
    await reply.code(200).send({
      asset_id: asset.id,
      project_id: asset.projectId,
      status: asset.status,
      source_type: asset.sourceType,
      encoding_tier: asset.encodingTier,
      duration_sec: asset.durationSec ?? null,
      width: asset.width ?? null,
      height: asset.height ?? null,
      frame_rate: asset.frameRate ?? null,
      audio_codec: asset.audioCodec ?? null,
      video_codec: asset.videoCodec ?? null,
      selected_offering: asset.selectedOffering ?? null,
      created_at: asset.createdAt.toISOString(),
      ready_at: asset.readyAt?.toISOString() ?? null,
      deleted_at: asset.deletedAt?.toISOString() ?? null,
      error_message: asset.errorMessage ?? null,
      playback_id: playback?.id ?? null,
      playback_url: playback ? `/v1/playback/${encodeURIComponent(playback.id)}` : null,
      billing: serializeCharge(charge),
      renditions: renditions.map((row) => ({
        id: row.id,
        resolution: row.resolution,
        codec: row.codec,
        bitrate_kbps: row.bitrateKbps,
        storage_key: row.storageKey ?? null,
        status: row.status,
        duration_sec: row.durationSec ?? null,
        completed_at: row.completedAt?.toISOString() ?? null,
      })),
      jobs: jobs.map((row) => ({
        id: row.id,
        kind: row.kind,
        status: row.status,
        worker_url: row.workerUrl ?? null,
        error_message: row.errorMessage ?? null,
        started_at: row.startedAt?.toISOString() ?? null,
        completed_at: row.completedAt?.toISOString() ?? null,
      })),
      include_deleted: includeDeleted,
    });
  });

  app.delete("/v1/videos/assets/:id", async (req, reply) => {
    if (!deps.assetsRepo) {
      await reply.code(501).send({ error: "asset_repo_unavailable" });
      return;
    }
    const { id } = req.params as { id: string };
    const asset = await deps.assetsRepo.byId(id);
    if (!asset) {
      await reply.code(404).send({ error: "asset_not_found" });
      return;
    }
    await deps.assetsRepo.softDelete(id, new Date());
    req.log.info({ asset_id: id }, "soft-delete asset");
    await reply.code(204).send();
  });
}

function serializeCharge(charge: ChargeSummary | null): Record<string, unknown> | null {
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
