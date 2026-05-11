import { randomUUID } from "node:crypto";

import { MODE } from "../livepeer/capabilityMap.js";
import { HEADER, SPEC_VERSION } from "../livepeer/headers.js";
import { newRequestId } from "../livepeer/requestId.js";
import type { VideoRouteCandidate, VideoRouteSelector } from "../livepeer/routeSelector.js";
import type { AssetRepo, LiveStreamRepo } from "../repo/index.js";
import type { MutableEncodingJobRepo } from "../repo/encodingJobs.js";
import type { MutableRenditionRepo } from "../repo/renditions.js";
import type { PlaybackIdRepo } from "../repo/playbackIds.js";
import type { RecordingRepo } from "../repo/recordings.js";
import type { StorageProvider } from "../engine/interfaces/storageProvider.js";
import type { Asset, Codec, EncodingTier, Resolution } from "../engine/types/index.js";
import type { UsageLedger } from "./usageLedger.js";

type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

export interface AbrExecutionManagerDeps {
  assets: AssetRepo;
  jobs: MutableEncodingJobRepo;
  renditions: MutableRenditionRepo;
  playbackIds: PlaybackIdRepo;
  recordings: RecordingRepo;
  liveStreams: LiveStreamRepo;
  storage: StorageProvider;
  routeSelector: VideoRouteSelector;
  usageLedger?: UsageLedger;
  fetchImpl?: FetchLike;
  logger?: Pick<Console, "info" | "error" | "warn">;
}

export interface SubmitAbrAssetInput {
  assetId: string;
  route: VideoRouteCandidate;
}

export interface RecordingHandoffInput {
  liveStreamId: string;
  recordingId: string;
  projectId: string;
  sourceUrl: string;
  endedAt: Date;
}

export interface AbrExecutionManager {
  submitAsset(input: SubmitAbrAssetInput): Promise<{ executionId: string }>;
  retryAsset(assetId: string): Promise<{ executionId: string }>;
  handoffRecording(input: RecordingHandoffInput): Promise<{ assetId: string; executionId: string }>;
}

interface AbrPreset {
  preset: string;
  renditions: Array<{
    name: string;
    resolution: Resolution;
    codec: Codec;
    bitrateKbps: number;
  }>;
  auxRenditions?: Array<{
    name: string;
  }>;
}

interface OutputTarget {
  name: string;
  playlistKey: string;
  streamKey: string;
  playlistUrl: string;
  streamUrl: string;
}

interface SubmittedJob {
  executionId: string;
  nativeJobId: string;
  workerBaseUrl: string;
  route: VideoRouteCandidate;
  assetId: string;
  recordingId?: string;
  manifestKey: string;
  playbackId: string;
  renditionTargets: OutputTarget[];
  preset: AbrPreset;
}

interface AbrRunnerStatus {
  job_id: string;
  status: string;
  phase?: string;
  manifest_url?: string;
  input?: {
    duration?: number;
    width?: number;
    height?: number;
    fps?: number;
    video_codec?: string;
    audio_codec?: string;
  };
  renditions?: Array<{
    name: string;
    status: string;
    bitrate?: number;
    file_size?: number;
  }>;
  error?: string;
  error_code?: string;
  processing_time_seconds?: number;
}

const TIER_PRESET: Record<EncodingTier, AbrPreset> = {
  baseline: {
    preset: "abr-mobile",
    renditions: [
      { name: "720p", resolution: "720p", codec: "h264", bitrateKbps: 2500 },
      { name: "480p", resolution: "480p", codec: "h264", bitrateKbps: 1000 },
      { name: "360p", resolution: "360p", codec: "h264", bitrateKbps: 600 },
    ],
  },
  standard: {
    preset: "abr-standard",
    renditions: [
      { name: "1080p", resolution: "1080p", codec: "h264", bitrateKbps: 5000 },
      { name: "720p", resolution: "720p", codec: "h264", bitrateKbps: 2500 },
      { name: "480p", resolution: "480p", codec: "h264", bitrateKbps: 1000 },
      { name: "360p", resolution: "360p", codec: "h264", bitrateKbps: 600 },
    ],
  },
  premium: {
    preset: "abr-premium",
    renditions: [
      { name: "2160p", resolution: "2160p", codec: "h264", bitrateKbps: 15000 },
      { name: "1080p", resolution: "1080p", codec: "h264", bitrateKbps: 5000 },
      { name: "720p", resolution: "720p", codec: "h264", bitrateKbps: 2500 },
      { name: "480p", resolution: "480p", codec: "h264", bitrateKbps: 1000 },
      { name: "360p", resolution: "360p", codec: "h264", bitrateKbps: 600 },
    ],
    auxRenditions: [{ name: "audio-only" }],
  },
};

const POLL_INTERVAL_MS = 3000;
const POLL_TIMEOUT_MS = 30 * 60 * 1000;

export function createAbrExecutionManager(deps: AbrExecutionManagerDeps): AbrExecutionManager {
  const fetchImpl = deps.fetchImpl ?? fetch;
  return {
    async submitAsset(input) {
      const asset = await deps.assets.byId(input.assetId);
      if (!asset) throw new Error(`asset ${input.assetId} not found`);
      const execution = await initializeExecution(deps, fetchImpl, asset, input.route);
      void runExecution(deps, fetchImpl, execution);
      return { executionId: execution.executionId };
    },

    async retryAsset(assetId) {
      const asset = await deps.assets.byId(assetId);
      if (!asset) throw new Error(`asset ${assetId} not found`);
      if (asset.deletedAt) throw new Error(`asset ${assetId} is deleted`);
      const [route] = await deps.routeSelector.select({
        capability: "video:transcode.abr",
        offering: asset.selectedOffering ?? "default",
      });
      if (!route) {
        throw new Error(`no video:transcode.abr route available for asset ${assetId}`);
      }
      const execution = await initializeExecution(deps, fetchImpl, asset, route);
      void runExecution(deps, fetchImpl, execution);
      return { executionId: execution.executionId };
    },

    async handoffRecording(input) {
      const now = new Date();
      const assetId = `asset_${randomUUID().replaceAll("-", "").slice(0, 16)}`;
      await deps.assets.insert({
        id: assetId,
        projectId: input.projectId,
        status: "queued",
        sourceType: "live_recording",
        sourceUrl: input.sourceUrl,
        encodingTier: "standard",
        createdAt: now,
      });
      await deps.recordings.updateStatus(input.recordingId, "pending", {
        assetId,
        endedAt: input.endedAt,
      });
      await deps.liveStreams.updateStatus(input.liveStreamId, "ended", {
        recordingAssetId: assetId,
      });
      const [route] = await deps.routeSelector.select({
        capability: "video:transcode.abr",
        offering: "default",
      });
      if (!route) {
        await deps.recordings.updateStatus(input.recordingId, "failed", { assetId });
        await deps.assets.updateStatus(assetId, "errored", {
          errorMessage: "recording_handoff_failed: no video:transcode.abr route available",
        });
        throw new Error("no video:transcode.abr route available for recording handoff");
      }
      const asset = await deps.assets.byId(assetId);
      if (!asset) throw new Error(`recording handoff asset ${assetId} missing after insert`);
      const execution = await initializeExecution(deps, fetchImpl, asset, route, input.recordingId);
      void runExecution(deps, fetchImpl, execution);
      return { assetId, executionId: execution.executionId };
    },
  };
}

async function initializeExecution(
  deps: AbrExecutionManagerDeps,
  fetchImpl: FetchLike,
  asset: Asset,
  route: VideoRouteCandidate,
  recordingId?: string,
): Promise<SubmittedJob> {
  const preset = TIER_PRESET[asset.encodingTier];
  const workerBaseUrl = inferWorkerBaseUrl(route);
  const inputUrl = await resolveInputUrl(deps.storage, asset);
  const manifestTarget = await deps.storage.putSignedUploadUrl({
    assetId: asset.id,
    kind: "manifest",
    filename: "master.m3u8",
    contentType: "application/vnd.apple.mpegurl",
    expiresInSec: 3600,
  });
  const playbackId = `play_${asset.id}`;
  await deps.jobs.deleteByAsset(asset.id);
  await deps.renditions.deleteByAsset(asset.id);
  await deps.playbackIds.deleteByAsset(asset.id);

  const renditionTargets: OutputTarget[] = [];
  for (const rendition of preset.renditions) {
    const renditionSlug = sanitizeSlug(rendition.name);
    const playlist = await deps.storage.putSignedUploadUrl({
      assetId: asset.id,
      kind: "manifest",
      filename: `${renditionSlug}/playlist.m3u8`,
      contentType: "application/vnd.apple.mpegurl",
      expiresInSec: 3600,
    });
    const stream = await deps.storage.putSignedUploadUrl({
      assetId: asset.id,
      kind: "rendition",
      filename: `${renditionSlug}/stream.mp4`,
      contentType: "video/mp4",
      expiresInSec: 3600,
    });
    renditionTargets.push({
      name: rendition.name,
      playlistKey: playlist.storageKey,
      streamKey: stream.storageKey,
      playlistUrl: playlist.url,
      streamUrl: stream.url,
    });
    await deps.renditions.insert({
      id: `rend_${randomUUID().replaceAll("-", "").slice(0, 16)}`,
      assetId: asset.id,
      resolution: rendition.resolution,
      codec: rendition.codec,
      bitrateKbps: rendition.bitrateKbps,
      storageKey: stream.storageKey,
      status: "queued",
      createdAt: new Date(),
    });
  }

  for (const aux of preset.auxRenditions ?? []) {
    const renditionSlug = sanitizeSlug(aux.name);
    const playlist = await deps.storage.putSignedUploadUrl({
      assetId: asset.id,
      kind: "manifest",
      filename: `${renditionSlug}/playlist.m3u8`,
      contentType: "application/vnd.apple.mpegurl",
      expiresInSec: 3600,
    });
    const stream = await deps.storage.putSignedUploadUrl({
      assetId: asset.id,
      kind: "rendition",
      filename: `${renditionSlug}/stream.mp4`,
      contentType: "audio/mp4",
      expiresInSec: 3600,
    });
    renditionTargets.push({
      name: aux.name,
      playlistKey: playlist.storageKey,
      streamKey: stream.storageKey,
      playlistUrl: playlist.url,
      streamUrl: stream.url,
    });
  }

  const executionId = `job_${randomUUID().replaceAll("-", "").slice(0, 16)}`;
  await deps.jobs.insert({
    id: executionId,
    assetId: asset.id,
    kind: "encode",
    status: "queued",
    inputUrl,
    outputPrefix: manifestTarget.storageKey,
    workerUrl: workerBaseUrl,
  });
  await deps.playbackIds.insert({
    id: playbackId,
    projectId: asset.projectId,
    assetId: asset.id,
    liveStreamId: null,
    policy: "public",
    tokenRequired: false,
  });
  await deps.assets.updateStatus(asset.id, "queued", {
    selectedOffering: route.offering,
    errorMessage: undefined,
  });

  const submitUrl = `${workerBaseUrl.replace(/\/$/, "")}/v1/video/transcode/abr`;
  const payload = {
    input_url: inputUrl,
    preset: preset.preset,
    output_urls: {
      manifest: manifestTarget.url,
      renditions: Object.fromEntries(
        renditionTargets.map((target) => [
          target.name,
          {
            playlist: target.playlistUrl,
            stream: target.streamUrl,
          },
        ]),
      ),
    },
  };
  const submitRes = await fetchImpl(submitUrl, {
    method: "POST",
    headers: buildWorkerHeaders(route),
    body: JSON.stringify(payload),
  });
  if (!submitRes.ok) {
    const message = await safeResponseText(submitRes);
    await failExecution(deps, executionId, asset.id, renditionTargets, recordingId, `worker_submit_failed: ${submitRes.status} ${message}`);
    throw new Error(`ABR submit failed: ${submitRes.status} ${message}`);
  }
  const submitBody = (await submitRes.json()) as { job_id?: string };
  if (!submitBody.job_id) {
    await failExecution(deps, executionId, asset.id, renditionTargets, recordingId, "worker_submit_failed: missing job_id");
    throw new Error("ABR submit returned malformed response");
  }
  await deps.jobs.updateStatus(executionId, "running", {
    startedAt: new Date(),
    workerUrl: workerBaseUrl,
  });
  return {
    executionId,
    nativeJobId: submitBody.job_id,
    workerBaseUrl,
    route,
    assetId: asset.id,
    recordingId,
    manifestKey: manifestTarget.storageKey,
    playbackId,
    renditionTargets,
    preset,
  };
}

async function runExecution(
  deps: AbrExecutionManagerDeps,
  fetchImpl: FetchLike,
  job: SubmittedJob,
): Promise<void> {
  const startedAt = Date.now();
  for (;;) {
    if (Date.now() - startedAt > POLL_TIMEOUT_MS) {
      await failExecution(deps, job.executionId, job.assetId, job.renditionTargets, job.recordingId, "worker_timeout: ABR job did not complete before timeout");
      return;
    }
    const statusUrl = `${job.workerBaseUrl.replace(/\/$/, "")}/v1/video/transcode/abr/status`;
    try {
      const res = await fetchImpl(statusUrl, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ job_id: job.nativeJobId }),
      });
      if (!res.ok) {
        const message = await safeResponseText(res);
        throw new Error(`status ${res.status}: ${message}`);
      }
      const body = (await res.json()) as AbrRunnerStatus;
      await updateProgress(deps, job.assetId, body);
      if (body.status === "complete") {
        await completeExecution(deps, job, body);
        return;
      }
      if (body.status === "error") {
        await failExecution(
          deps,
          job.executionId,
          job.assetId,
          job.renditionTargets,
          job.recordingId,
          `${body.error_code ?? "worker_error"}: ${body.error ?? "ABR job failed"}`,
        );
        return;
      }
    } catch (err) {
      deps.logger?.warn?.("video-gateway: abr status poll failed");
      if (Date.now() - startedAt > POLL_TIMEOUT_MS / 4) {
        await failExecution(
          deps,
          job.executionId,
          job.assetId,
          job.renditionTargets,
          job.recordingId,
          `worker_status_failed: ${stringifyError(err)}`,
        );
        return;
      }
    }
    await sleep(POLL_INTERVAL_MS);
  }
}

async function updateProgress(
  deps: AbrExecutionManagerDeps,
  assetId: string,
  body: AbrRunnerStatus,
): Promise<void> {
  const rows = await deps.renditions.byAsset(assetId);
  const rowByResolution = new Map(rows.map((row) => [row.resolution, row]));
  for (const rendition of body.renditions ?? []) {
    const resolution = renditionNameToResolution(rendition.name);
    if (!resolution) continue;
    const row = rowByResolution.get(resolution);
    if (!row) continue;
    const status = mapRunnerRenditionStatus(rendition.status);
    await deps.renditions.updateStatus(row.id, status, {
      bitrateKbps: rendition.bitrate ?? row.bitrateKbps,
    });
  }
}

async function completeExecution(
  deps: AbrExecutionManagerDeps,
  job: SubmittedJob,
  body: AbrRunnerStatus,
): Promise<void> {
  const readyAt = new Date();
  const rows = await deps.renditions.byAsset(job.assetId);
  const rowByResolution = new Map(rows.map((row) => [row.resolution, row]));
  for (const rendition of body.renditions ?? []) {
    const resolution = renditionNameToResolution(rendition.name);
    if (!resolution) continue;
    const row = rowByResolution.get(resolution);
    if (!row) continue;
    const target = job.renditionTargets.find((entry) => entry.name === rendition.name);
    await deps.renditions.updateStatus(row.id, "completed", {
      bitrateKbps: rendition.bitrate ?? row.bitrateKbps,
      storageKey: target?.streamKey ?? row.storageKey,
      durationSec: body.input?.duration,
      completedAt: readyAt,
    });
  }
  await deps.jobs.updateStatus(job.executionId, "completed", {
    completedAt: readyAt,
    outputPrefix: job.manifestKey,
    errorMessage: undefined,
  });
  await deps.assets.updateStatus(job.assetId, "ready", {
    durationSec: body.input?.duration,
    width: body.input?.width,
    height: body.input?.height,
    frameRate: body.input?.fps,
    videoCodec: body.input?.video_codec,
    audioCodec: body.input?.audio_codec,
    readyAt,
    errorMessage: undefined,
  });
  const asset = await deps.assets.byId(job.assetId);
  if (asset && body.input?.duration && deps.usageLedger) {
    await deps.usageLedger.recordVodUsage({
      projectId: asset.projectId,
      assetId: asset.id,
      encodingTier: asset.encodingTier,
      durationSec: body.input.duration,
    });
  }
  if (job.recordingId) {
    await deps.recordings.updateStatus(job.recordingId, "ready", {
      assetId: job.assetId,
    });
  }
}

async function failExecution(
  deps: AbrExecutionManagerDeps,
  executionId: string,
  assetId: string,
  targets: OutputTarget[],
  recordingId: string | undefined,
  message: string,
): Promise<void> {
  const failedAt = new Date();
  const rows = await deps.renditions.byAsset(assetId);
  for (const row of rows) {
    await deps.renditions.updateStatus(row.id, "failed", {
      completedAt: failedAt,
    });
  }
  await deps.jobs.updateStatus(executionId, "failed", {
    completedAt: failedAt,
    errorMessage: message,
  });
  await deps.assets.updateStatus(assetId, "errored", {
    errorMessage: message,
  });
  const asset = await deps.assets.byId(assetId);
  if (asset && deps.usageLedger) {
    await deps.usageLedger.refundVodUsage({
      projectId: asset.projectId,
      assetId,
    });
  }
  if (recordingId) {
    await deps.recordings.updateStatus(recordingId, "failed", {
      assetId,
    });
  }
  await Promise.allSettled(
    targets.flatMap((target) => [
      deps.storage.delete(target.playlistKey),
      deps.storage.delete(target.streamKey),
    ]),
  );
}

async function resolveInputUrl(storage: StorageProvider, asset: Asset): Promise<string> {
  if (!asset.sourceUrl) throw new Error(`asset ${asset.id} has no source_url`);
  if (/^https?:\/\//.test(asset.sourceUrl)) {
    return asset.sourceUrl;
  }
  return storage.getSignedDownloadUrl({
    storageKey: asset.sourceUrl,
    expiresInSec: 3600,
  });
}

function inferWorkerBaseUrl(route: VideoRouteCandidate): string {
  const extra = isObject(route.extra) ? route.extra : null;
  const direct = pickString(extra?.["abr_runner_url"])
    ?? pickString(extra?.["runner_url"])
    ?? pickString(extra?.["backend_url"])
    ?? pickString(extra?.["worker_api_url"]);
  return direct ?? route.brokerUrl;
}

function buildWorkerHeaders(route: VideoRouteCandidate): HeadersInit {
  return {
    "Content-Type": "application/json",
    [HEADER.CAPABILITY]: "video:transcode.abr",
    [HEADER.OFFERING]: route.offering,
    [HEADER.MODE]: MODE.HTTP_REQRESP,
    [HEADER.SPEC_VERSION]: SPEC_VERSION,
    [HEADER.REQUEST_ID]: newRequestId(),
  };
}

function sanitizeSlug(value: string): string {
  return value.replace(/[^A-Za-z0-9._-]+/g, "-");
}

function renditionNameToResolution(name: string): Resolution | null {
  const match = name.match(/(2160p|1080p|720p|480p|360p|240p)/);
  return (match?.[1] as Resolution | undefined) ?? null;
}

function mapRunnerRenditionStatus(status: string): "queued" | "running" | "completed" | "failed" {
  switch (status) {
    case "complete":
      return "completed";
    case "error":
      return "failed";
    case "encoding":
    case "uploading":
      return "running";
    default:
      return "queued";
  }
}

function pickString(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 ? value : null;
}

function isObject(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === "object" && !Array.isArray(value);
}

function stringifyError(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

async function safeResponseText(res: Response): Promise<string> {
  try {
    return (await res.text()).trim();
  } catch {
    return "";
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
