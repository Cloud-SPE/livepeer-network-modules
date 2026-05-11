import { createHash } from "node:crypto";

import type { FastifyInstance } from "fastify";
import { z } from "zod";

import type { Config } from "../config.js";
import type { LiveStreamRepo } from "../engine/index.js";
import type { LiveSessionDirectory } from "../livepeer/liveSessionDirectory.js";
import { openRtmpSession } from "../livepeer/rtmp-adapter.js";
import type { VideoRouteSelector } from "../livepeer/routeSelector.js";
import { buildLiveSelectionHints } from "../livepeer/selectionPolicy.js";
import {
  selectAbrLadder,
  type CustomerTier,
} from "../service/abrSelector.js";
import type { RecordingRepo } from "../repo/index.js";
import type { AbrExecutionManager } from "../service/abrExecution.js";
import type { ChargeSummary, UsageLedger } from "../service/usageLedger.js";

const CreateLiveStreamBody = z.object({
  project_id: z.string().min(1),
  name: z.string().trim().min(1).max(160).optional(),
  recording_enabled: z.boolean().optional(),
  record_to_vod: z.boolean().optional(),
  offering: z.string().optional(),
  customer_tier: z.enum(["free", "prepaid", "enterprise"]).optional(),
});

export interface LiveStreamsDeps {
  cfg: Config;
  routeSelector: VideoRouteSelector;
  liveSessions: LiveSessionDirectory;
  liveStreamsRepo?: LiveStreamRepo;
  recordingsRepo?: RecordingRepo;
  execution?: AbrExecutionManager;
  usageLedger?: UsageLedger;
  projectExists?: (projectId: string) => Promise<boolean>;
}

export interface ProvisionLiveStreamInput {
  projectId: string;
  name?: string;
  offering?: string;
  customerTier?: CustomerTier;
  recordToVod?: boolean;
  requestHeaders?: Record<string, string | string[] | undefined>;
}

export interface ProvisionLiveStreamResult {
  streamId: string;
  name: string;
  sessionId: string;
  rtmpPushUrl: string;
  streamKey: string;
  hlsPlaybackUrl: string;
  recordToVod: boolean;
  customerTier: CustomerTier;
  createdAt: Date;
  abrLadder: Array<{
    resolution: string;
    codec: string;
    bitrateKbps: number;
  }>;
  expiresAt: string;
  requestId: string;
}

export function registerLiveStreams(app: FastifyInstance, deps: LiveStreamsDeps): void {
  app.post("/v1/live/streams", async (req, reply) => {
    const parsed = CreateLiveStreamBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.issues });
      return;
    }
    if (deps.projectExists && !(await deps.projectExists(parsed.data.project_id))) {
      await reply.code(404).send({ error: "project_not_found" });
      return;
    }
    const stream = await provisionLiveStream(deps, {
      projectId: parsed.data.project_id,
      name: parsed.data.name,
      offering: parsed.data.offering,
      customerTier: parsed.data.customer_tier,
      recordToVod: parsed.data.record_to_vod ?? parsed.data.recording_enabled,
      requestHeaders: req.headers,
    });
    if (stream.recordToVod && deps.recordingsRepo) {
      const existing = await deps.recordingsRepo.byLiveStream(stream.streamId);
      const hasOpen = existing.some((row) => row.endedAt === null);
      if (!hasOpen) {
        await deps.recordingsRepo.insert({
          id: `rec_${randomHex16()}`,
          liveStreamId: stream.streamId,
          assetId: null,
          status: "running",
          startedAt: stream.createdAt,
          endedAt: null,
        });
      }
    }

    await reply.code(201).send({
      stream_id: stream.streamId,
      project_id: parsed.data.project_id,
      name: stream.name,
      session_id: stream.sessionId,
      rtmp_push_url: stream.rtmpPushUrl,
      stream_key: stream.streamKey,
      hls_playback_url: stream.hlsPlaybackUrl,
      record_to_vod: stream.recordToVod,
      customer_tier: stream.customerTier,
      abr_ladder: stream.abrLadder.map((r) => ({
        resolution: r.resolution,
        codec: r.codec,
        bitrate_kbps: r.bitrateKbps,
      })),
      expires_at: stream.expiresAt,
      request_id: stream.requestId,
    });
  });

  app.get("/v1/live/streams/:id", async (req, reply) => {
    const { id } = req.params as { id: string };
    if (!deps.liveStreamsRepo) {
      await reply.code(501).send({ error: "live_stream_repo_unavailable" });
      return;
    }
    const stream = await deps.liveStreamsRepo.byId(id);
    if (!stream) {
      await reply.code(404).send({ error: "stream_not_found" });
      return;
    }
    const session = stream.sessionId ? deps.liveSessions.get(stream.sessionId) : deps.liveSessions.getByStreamId(id);
    const charge = deps.usageLedger ? await deps.usageLedger.getChargeByLiveStream(id) : null;
    await reply.code(200).send({
      stream_id: id,
      name: stream.name ?? id,
      project_id: stream.projectId,
      status: publicStatus(stream.status),
      session_id: stream.sessionId ?? null,
      playback_url: session?.hlsPlaybackUrl ?? null,
      record_to_vod: stream.recordingEnabled,
      created_at: stream.createdAt.toISOString(),
      ended_at: stream.endedAt?.toISOString() ?? null,
      cost_accrued_cents: charge?.committedAmountCents ?? 0,
      billing: serializeCharge(charge),
    });
  });

  app.post("/v1/live/streams/:id/end", async (req, reply) => {
    const { id } = req.params as { id: string };
    if (!deps.liveStreamsRepo) {
      await reply.code(501).send({ error: "live_stream_repo_unavailable" });
      return;
    }
    const stream = await deps.liveStreamsRepo.byId(id);
    if (!stream) {
      await reply.code(404).send({ error: "stream_not_found" });
      return;
    }
    if (stream.endedAt) {
      const charge = deps.usageLedger ? await deps.usageLedger.getChargeByLiveStream(id) : null;
      await reply.code(200).send({
        stream_id: id,
        status: "ended",
        ended_at: stream.endedAt.toISOString(),
        cost_accrued_cents: charge?.committedAmountCents ?? 0,
        billing: serializeCharge(charge),
        recording_asset_id: stream.recordingAssetId ?? null,
        recording_execution_id: null,
      });
      return;
    }
    const endedAt = new Date();
    await deps.liveStreamsRepo.updateStatus(id, "ended", {
      lastSeenAt: endedAt,
      endedAt,
    });
    if (stream.createdAt && deps.usageLedger) {
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
    const charge = deps.usageLedger ? await deps.usageLedger.getChargeByLiveStream(id) : null;
    await reply.code(200).send({
      stream_id: id,
      status: "ended",
      ended_at: endedAt.toISOString(),
      cost_accrued_cents: charge?.committedAmountCents ?? 0,
      billing: serializeCharge(charge),
      recording_asset_id: handoff?.assetId ?? stream.recordingAssetId ?? null,
      recording_execution_id: handoff?.executionId ?? null,
    });
  });
}

export async function provisionLiveStream(
  deps: LiveStreamsDeps,
  input: ProvisionLiveStreamInput,
): Promise<ProvisionLiveStreamResult> {
  const streamId = `live_${randomHex16()}`;
  const recordToVod = input.recordToVod ?? false;
  const customerTier: CustomerTier = input.customerTier ?? "free";
  const offering = input.offering ?? "default";
  const name = input.name?.trim() || streamId;

  const ladder = selectAbrLadder({
    customerTier,
    policy: deps.cfg.abrPolicy,
  });
  const selectionHints = buildLiveSelectionHints({
    customerTier,
    recordToVod,
    ladder,
  });
  const session = await openRtmpSession({
    cfg: deps.cfg,
    routeSelector: deps.routeSelector,
    callerId: input.projectId,
    offering,
    streamId,
    requestHeaders: input.requestHeaders,
    selectionHints,
  });

  const streamKey = parseStreamKey(session.brokerRtmpUrl);
  const createdAt = new Date();
  deps.liveSessions.record({
    streamId,
    sessionId: session.sessionId,
    brokerUrl: session.brokerUrl,
    brokerRtmpUrl: session.brokerRtmpUrl,
    streamKey,
    hlsPlaybackUrl: session.hlsUrl,
  });

  if (deps.liveStreamsRepo) {
    await deps.liveStreamsRepo.insert({
      id: streamId,
      projectId: input.projectId,
      name,
      streamKeyHash: hashStreamKey(streamKey),
      status: "active",
      ingestProtocol: "rtmp",
      recordingEnabled: recordToVod,
      sessionId: session.sessionId,
      workerUrl: session.brokerUrl,
      selectedCapability: "video:live.rtmp",
      selectedOffering: offering,
      lastSeenAt: createdAt,
      createdAt,
    });
  }

  return {
    streamId,
    name,
    sessionId: session.sessionId,
    rtmpPushUrl: session.brokerRtmpUrl,
    streamKey,
    hlsPlaybackUrl: session.hlsUrl,
    recordToVod,
    customerTier,
    createdAt,
    abrLadder: ladder,
    expiresAt: session.expiresAt,
    requestId: session.requestId,
  };
}

function randomHex16(): string {
  return Math.random().toString(16).slice(2, 18).padEnd(16, "0");
}

function parseStreamKey(rtmpUrl: string): string {
  const segments = rtmpUrl.split("/");
  return segments[segments.length - 1] ?? "";
}

function hashStreamKey(streamKey: string): string {
  return createHash("sha256").update(streamKey).digest("hex");
}

function publicStatus(status: string): string {
  if (status === "active" || status === "reconnecting") return "live";
  return status;
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

export async function maybeHandoffRecording(
  deps: Pick<LiveStreamsDeps, "liveSessions" | "recordingsRepo" | "execution">,
  input: {
    liveStreamId: string;
    projectId: string;
    sessionId?: string;
    endedAt: Date;
  },
): Promise<{ assetId: string; executionId: string } | null> {
  if (!deps.recordingsRepo || !deps.execution) return null;
  const recordings = await deps.recordingsRepo.byLiveStream(input.liveStreamId);
  const recording = recordings.find((row) => row.endedAt === null) ?? recordings[0] ?? null;
  if (!recording) return null;
  const sourceUrl =
    (input.sessionId ? deps.liveSessions.get(input.sessionId)?.hlsPlaybackUrl : null)
    ?? deps.liveSessions.getByStreamId(input.liveStreamId)?.hlsPlaybackUrl
    ?? null;
  if (!sourceUrl) {
    await deps.recordingsRepo.updateStatus(recording.id, "failed", {
      endedAt: input.endedAt,
    });
    return null;
  }
  if (recording.endedAt === null) {
    await deps.recordingsRepo.updateStatus(recording.id, "pending", {
      endedAt: input.endedAt,
    });
  }
  return deps.execution.handoffRecording({
    liveStreamId: input.liveStreamId,
    recordingId: recording.id,
    projectId: input.projectId,
    sourceUrl,
    endedAt: input.endedAt,
  });
}
