import type {
  Asset,
  Codec,
  EncodingJob,
  ProbeResult,
  RenditionSpec,
  Resolution,
  SelectedWorkerRoute,
} from "../types/index.js";
import type {
  EventBus,
  Logger,
  StorageProvider,
  Wallet,
  WorkerClient,
  WorkerResolver,
} from "../interfaces/index.js";
import type { AssetRepo, EncodingJobRepo, RenditionRepo } from "../repo/index.js";
import { VideoCoreError } from "../types/index.js";
import { estimateCost, reportUsage } from "./costQuoter.js";
import { buildMasterManifest, manifestRenditionsFromSpecs } from "./manifestBuilder.js";
import { expandTier, type EncodingLadder } from "../config/encodingLadder.js";
import type { PricingConfig } from "../config/pricing.js";

export interface OrchestratorDeps {
  assetRepo: AssetRepo;
  jobRepo: EncodingJobRepo;
  renditionRepo: RenditionRepo;
  storage: StorageProvider;
  wallet: Wallet;
  workerResolver: WorkerResolver;
  workerClient: WorkerClient;
  eventBus: EventBus;
  ladder: EncodingLadder;
  pricing: PricingConfig;
  logger?: Logger;
  callerId: string;
  callerTier: string;
  workerOffering: string;
  workerSelectionMinWeight?: number;
}

export interface EncodeResult {
  storageKey: string;
  durationSec: number;
  segmentCount?: number;
}

const RESERVATION_HANDLES = new Map<string, unknown>();

export async function probeAndSchedule(
  deps: OrchestratorDeps & { asset: Asset },
): Promise<void> {
  const {
    asset,
    assetRepo,
    storage,
    wallet,
    workerResolver,
    workerClient,
    ladder,
    pricing,
    callerId,
    logger,
  } = deps;

  const route = await workerResolver.selectWorker({
    capability: "video:transcode.vod",
    offering: deps.workerOffering,
    tier: deps.callerTier,
    ...(deps.workerSelectionMinWeight !== undefined
      ? { minWeight: deps.workerSelectionMinWeight }
      : {}),
  });
  if (!route) {
    await markErrored(
      deps,
      asset.id,
      "NoWorkersAvailable",
      "no workers can do video:transcode.vod",
    );
    return;
  }

  const sourceKey = storage.pathFor({
    assetId: asset.id,
    kind: "source",
    filename: asset.sourceUrl
      ? (asset.sourceUrl.split("/").pop() ?? "source")
      : "source.mp4",
  });
  const inputUrl =
    asset.sourceUrl?.startsWith("s3://") || asset.sourceUrl?.startsWith("http")
      ? asset.sourceUrl
      : await storage.getSignedDownloadUrl({
          storageKey: sourceKey,
          expiresInSec: 3600,
        });

  const probeJob = await deps.jobRepo.insert({
    id: `job_${makeId()}`,
    assetId: asset.id,
    kind: "probe",
    status: "queued",
    inputUrl,
    attemptCount: 0,
  });
  await deps.jobRepo.updateStatus(probeJob.id, "running", {
    workerUrl: route.workerUrl,
    startedAt: new Date(),
  });

  let probe: ProbeResult;
  try {
    probe = await workerClient.callWorker<
      { input_url: string; job_id: string },
      ProbeResult
    >({
      route,
      path: "/v1/video/transcode/probe",
      method: "POST",
      body: { input_url: inputUrl, job_id: probeJob.id },
      callerId,
      timeoutMs: 60_000,
    });
  } catch (err) {
    await deps.jobRepo.updateStatus(probeJob.id, "failed", {
      errorMessage: stringifyError(err),
      completedAt: new Date(),
    });
    await markErrored(
      deps,
      asset.id,
      "WorkerError",
      `probe failed: ${stringifyError(err)}`,
    );
    return;
  }

  await deps.jobRepo.updateStatus(probeJob.id, "completed", {
    completedAt: new Date(),
  });
  await assetRepo.updateStatus(asset.id, "preparing", {
    durationSec: probe.durationSec,
    width: probe.width,
    height: probe.height,
    frameRate: probe.frameRate,
    audioCodec: probe.audioCodec,
    videoCodec: probe.videoCodec,
    ffprobeJson: probe.raw,
  });

  const renditions = expandTier(asset.encodingTier, ladder);
  const quote = estimateCost({
    capability: "video:transcode.vod",
    callerTier: deps.callerTier,
    renditions,
    estimatedSeconds: probe.durationSec,
    pricing,
  });
  let handle: unknown = null;
  try {
    handle = await wallet.reserve(callerId, quote);
  } catch (err) {
    await markErrored(deps, asset.id, "WalletReserveFailed", stringifyError(err));
    return;
  }

  RESERVATION_HANDLES.set(asset.id, handle);

  const renditionRows: Array<{ id: string; spec: RenditionSpec }> = [];
  for (const r of renditions) {
    const row = await deps.renditionRepo.insert({
      id: `rend_${makeId()}`,
      assetId: asset.id,
      resolution: r.resolution,
      codec: r.codec,
      bitrateKbps: r.bitrateKbps,
      status: "queued",
    });
    renditionRows.push({ id: row.id, spec: r });
    await deps.jobRepo.insert({
      id: `job_${makeId()}`,
      assetId: asset.id,
      renditionId: row.id,
      kind: "encode",
      status: "queued",
      inputUrl,
      attemptCount: 0,
    });
  }
  await deps.jobRepo.insert({
    id: `job_${makeId()}`,
    assetId: asset.id,
    kind: "thumbnail",
    status: "queued",
    attemptCount: 0,
  });
  await deps.jobRepo.insert({
    id: `job_${makeId()}`,
    assetId: asset.id,
    kind: "finalize",
    status: "queued",
    attemptCount: 0,
  });

  logger?.info("orchestrator.scheduled", {
    asset_id: asset.id,
    renditions: renditions.length,
  });

  await runEncodePhase(deps, asset.id, route, inputUrl);
  await runFinalize(deps, asset.id, renditionRows);
}

async function runEncodePhase(
  deps: OrchestratorDeps,
  assetId: string,
  route: SelectedWorkerRoute,
  inputUrl: string,
): Promise<void> {
  const queued = await deps.jobRepo.queued(assetId, ["encode", "thumbnail"]);
  const cap = 4;
  let i = 0;
  async function worker(): Promise<void> {
    while (true) {
      const idx = i++;
      if (idx >= queued.length) return;
      const job = queued[idx];
      if (!job) return;
      await runOneJob(deps, job, route, inputUrl);
    }
  }
  await Promise.all(
    Array.from({ length: Math.min(cap, queued.length) }, () => worker()),
  );
}

async function runOneJob(
  deps: OrchestratorDeps,
  job: EncodingJob,
  route: SelectedWorkerRoute,
  inputUrl: string,
): Promise<void> {
  await deps.jobRepo.updateStatus(job.id, "running", {
    workerUrl: route.workerUrl,
    startedAt: new Date(),
  });
  try {
    if (job.kind === "encode") {
      if (!job.renditionId) throw new Error("encode job without rendition_id");
      const renditions = await deps.renditionRepo.byAsset(job.assetId);
      const rend = renditions.find((r) => r.id === job.renditionId);
      if (!rend) throw new Error("rendition not found");

      const outputPrefix = deps.storage.pathFor({
        assetId: job.assetId,
        kind: "rendition",
        codec: rend.codec,
        resolution: rend.resolution,
        filename: "",
      });

      const result = await deps.workerClient.callWorker<unknown, EncodeResult>({
        route,
        path: "/v1/video/transcode",
        method: "POST",
        body: {
          job_id: job.id,
          input_url: inputUrl,
          output_prefix: outputPrefix,
          codec: rend.codec,
          resolution: rend.resolution,
          bitrate_kbps: rend.bitrateKbps,
        },
        callerId: deps.callerId,
        timeoutMs: 30 * 60_000,
      });
      await deps.renditionRepo.updateStatus(rend.id, "completed", {
        storageKey: result.storageKey,
        durationSec: result.durationSec,
        completedAt: new Date(),
      });
      await deps.jobRepo.updateStatus(job.id, "completed", {
        completedAt: new Date(),
        outputPrefix,
      });
    } else if (job.kind === "thumbnail") {
      await deps.jobRepo.updateStatus(job.id, "completed", {
        completedAt: new Date(),
      });
    }
  } catch (err) {
    await deps.jobRepo.updateStatus(job.id, "failed", {
      errorMessage: stringifyError(err),
      completedAt: new Date(),
    });
    if (job.renditionId) {
      await deps.renditionRepo.updateStatus(job.renditionId, "failed", {
        completedAt: new Date(),
      });
    }
  }
}

async function runFinalize(
  deps: OrchestratorDeps,
  assetId: string,
  expected: Array<{ id: string; spec: RenditionSpec }>,
): Promise<void> {
  const all = await deps.renditionRepo.byAsset(assetId);
  const completed = all.filter((r) => r.status === "completed");
  const failedCount = all.filter((r) => r.status === "failed").length;

  if (failedCount > 0 || completed.length !== expected.length) {
    await markErrored(
      deps,
      assetId,
      "WorkerError",
      `rendition failure (${failedCount} failed)`,
    );
    const handle = RESERVATION_HANDLES.get(assetId);
    if (handle !== undefined && handle !== null) {
      await safeRefund(deps.wallet, handle);
    }
    RESERVATION_HANDLES.delete(assetId);
    return;
  }

  const manifestRenditions = manifestRenditionsFromSpecs(
    completed.map((r) => ({
      resolution: r.resolution,
      codec: r.codec,
      bitrateKbps: r.bitrateKbps,
    })),
    (s: RenditionSpec) => `${s.codec}/${s.resolution}/playlist.m3u8`,
  );
  const masterBody = buildMasterManifest(manifestRenditions);
  const masterKey = deps.storage.pathFor({
    assetId,
    kind: "manifest",
    filename: "master.m3u8",
  });
  await deps.storage.putObject(masterKey, masterBody, {
    contentType: "application/vnd.apple.mpegurl",
  });

  const asset = await deps.assetRepo.byId(assetId);
  const handle = RESERVATION_HANDLES.get(assetId);
  if (handle !== undefined && handle !== null && asset?.durationSec) {
    const usage = reportUsage({
      capability: "video:transcode.vod",
      renditions: completed.map((r) => ({
        resolution: r.resolution as Resolution,
        codec: r.codec as Codec,
        bitrateKbps: r.bitrateKbps,
      })),
      actualSeconds: asset.durationSec,
      pricing: deps.pricing,
    });
    try {
      await deps.wallet.commit(handle, usage);
    } catch (err) {
      deps.logger?.error("orchestrator.commit_failed", {
        asset_id: assetId,
        error: stringifyError(err),
      });
    }
  }
  RESERVATION_HANDLES.delete(assetId);

  await deps.assetRepo.updateStatus(assetId, "ready", { readyAt: new Date() });
  await deps.eventBus.emit(deps.callerId, {
    type: "video.asset.ready",
    occurredAt: new Date(),
    data: { asset_id: assetId, master_storage_key: masterKey },
  });
  deps.logger?.info("orchestrator.asset_ready", { asset_id: assetId });
}

async function markErrored(
  deps: OrchestratorDeps,
  assetId: string,
  code: string,
  message: string,
): Promise<void> {
  await deps.assetRepo.updateStatus(assetId, "errored", {
    errorMessage: `${code}: ${message}`,
  });
  await deps.eventBus
    .emit(deps.callerId, {
      type: "video.asset.errored",
      occurredAt: new Date(),
      data: { asset_id: assetId, code, message },
    })
    .catch(() => undefined);
  deps.logger?.error("orchestrator.errored", {
    asset_id: assetId,
    code,
    message,
  });
}

async function safeRefund(wallet: Wallet, handle: unknown): Promise<void> {
  try {
    await wallet.refund(handle);
  } catch {
    /* best-effort */
  }
}

function makeId(): string {
  return Math.random().toString(16).slice(2, 18).padEnd(16, "0");
}

function stringifyError(err: unknown): string {
  if (err instanceof VideoCoreError) return `${err.code}: ${err.message}`;
  if (err instanceof Error) return err.message;
  return String(err);
}
