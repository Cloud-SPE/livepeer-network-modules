import type { auth, billing } from "@livepeer-rewrite/customer-portal";

import { Capability } from "../livepeer/capabilityMap.js";
import { loadRateCardSnapshot, type Queryable } from "../repo/rateCard.js";
import {
  computeAudioSpeechActualCost,
  computeAudioTranscriptActualCost,
  computeEmbeddingsActualCost,
  computeImagesActualCost,
  estimateAudioSpeechReservation,
  estimateAudioTranscriptReservation,
  estimateEmbeddingsReservation,
  estimateImagesReservation,
} from "./pricing/cost.js";
import type { ImageQuality, RateCardSnapshot } from "./pricing/types.js";

type Caller = auth.Caller;
type ReservationHandle = billing.ReservationHandle;
type Wallet = billing.Wallet;

export interface EmbeddingsBillingBody {
  model?: string;
  input?: unknown;
}

export interface ImagesBillingBody {
  model?: string;
  size?: string;
  quality?: string;
  n?: number;
}

export interface AudioSpeechBillingBody {
  model?: string;
  input?: unknown;
}

export interface CreateNonChatBillingServiceInput {
  wallet: Wallet;
  rateCardStore: Queryable;
  cacheTtlMs?: number;
  now?: () => number;
}

export function createNonChatBillingService(input: CreateNonChatBillingServiceInput) {
  const ttl = input.cacheTtlMs ?? 30_000;
  const now = input.now ?? Date.now;
  let cached: { snapshot: RateCardSnapshot; loadedAt: number } | null = null;

  async function getSnapshot(): Promise<RateCardSnapshot> {
    if (cached && now() - cached.loadedAt < ttl) return cached.snapshot;
    const snapshot = await loadRateCardSnapshot(input.rateCardStore);
    cached = { snapshot, loadedAt: now() };
    return snapshot;
  }

  return {
    async reserveEmbeddings(
      caller: Caller,
      workId: string,
      model: string,
      body: EmbeddingsBillingBody,
    ): Promise<ReservationHandle | null> {
      const callerTier = asCallerTier(caller.tier);
      const snapshot = await getSnapshot();
      const estimate = estimateEmbeddingsReservation(normalizeEmbeddingInputs(body.input), model, snapshot);
      return input.wallet.reserve(caller.id, {
        workId,
        cents: estimate.estCents,
        estimatedTokens: estimate.promptEstimateTokens,
        model,
        capability: Capability.Embeddings,
        callerTier,
      });
    },

    async commitEmbeddings(
      handle: ReservationHandle | null,
      model: string,
      rawBody: ArrayBuffer,
      workUnits: number,
    ): Promise<number> {
      const usage = parseEmbeddingsUsage(Buffer.from(rawBody).toString("utf8"), workUnits);
      if (!handle) return usage.totalTokens;
      const cost = computeEmbeddingsActualCost(usage.promptTokens, model, await getSnapshot());
      await input.wallet.commit(handle, {
        cents: cost.actualCents,
        actualTokens: usage.totalTokens,
        model,
        capability: Capability.Embeddings,
      });
      return usage.totalTokens;
    },

    async reserveImages(
      caller: Caller,
      workId: string,
      model: string,
      body: ImagesBillingBody,
    ): Promise<ReservationHandle | null> {
      const callerTier = asCallerTier(caller.tier);
      const snapshot = await getSnapshot();
      const n = normalizeImageCount(body.n);
      const size = normalizeImageSize(body.size);
      const quality = normalizeImageQuality(body.quality);
      const estimate = estimateImagesReservation(n, model, size, quality, snapshot);
      return input.wallet.reserve(caller.id, {
        workId,
        cents: estimate.estCents,
        estimatedTokens: estimate.n,
        model,
        capability: Capability.ImagesGenerations,
        callerTier,
      });
    },

    async commitImages(
      handle: ReservationHandle | null,
      model: string,
      body: ImagesBillingBody,
      rawBody: ArrayBuffer,
    ): Promise<number> {
      const returnedCount = parseImagesCount(Buffer.from(rawBody).toString("utf8"));
      if (!handle) return returnedCount;
      const cost = computeImagesActualCost(
        returnedCount,
        model,
        normalizeImageSize(body.size),
        normalizeImageQuality(body.quality),
        await getSnapshot(),
      );
      await input.wallet.commit(handle, {
        cents: cost.actualCents,
        actualTokens: returnedCount,
        model,
        capability: Capability.ImagesGenerations,
      });
      return returnedCount;
    },

    async reserveTranscription(
      caller: Caller,
      workId: string,
      model: string,
      bodySizeBytes: number,
    ): Promise<ReservationHandle | null> {
      const callerTier = asCallerTier(caller.tier);
      const snapshot = await getSnapshot();
      const estimate = estimateAudioTranscriptReservation(bodySizeBytes, model, snapshot);
      return input.wallet.reserve(caller.id, {
        workId,
        cents: estimate.estCents,
        estimatedTokens: estimate.estimatedSeconds,
        model,
        capability: Capability.AudioTranscriptions,
        callerTier,
      });
    },

    async commitTranscription(
      handle: ReservationHandle | null,
      model: string,
      bodySizeBytes: number,
      workUnits: number,
    ): Promise<number> {
      const seconds = workUnits > 0
        ? workUnits
        : estimateAudioTranscriptReservation(bodySizeBytes, model, await getSnapshot()).estimatedSeconds;
      if (!handle) return seconds;
      const cost = computeAudioTranscriptActualCost(seconds, model, await getSnapshot());
      await input.wallet.commit(handle, {
        cents: cost.actualCents,
        actualTokens: seconds,
        model,
        capability: Capability.AudioTranscriptions,
      });
      return seconds;
    },

    async refund(handle: ReservationHandle | null): Promise<void> {
      if (!handle) return;
      await input.wallet.refund(handle);
    },

    async reserveSpeech(
      caller: Caller,
      workId: string,
      model: string,
      body: AudioSpeechBillingBody,
    ): Promise<ReservationHandle | null> {
      const callerTier = asCallerTier(caller.tier);
      const snapshot = await getSnapshot();
      const estimate = estimateAudioSpeechReservation(normalizeSpeechInput(body.input).length, model, snapshot);
      return input.wallet.reserve(caller.id, {
        workId,
        cents: estimate.estCents,
        estimatedTokens: estimate.charCount,
        model,
        capability: Capability.AudioSpeech,
        callerTier,
      });
    },

    async commitSpeech(
      handle: ReservationHandle | null,
      model: string,
      body: AudioSpeechBillingBody,
      workUnits: number,
    ): Promise<number> {
      const chars = workUnits > 0 ? workUnits : normalizeSpeechInput(body.input).length;
      if (!handle) return chars;
      const cost = computeAudioSpeechActualCost(chars, model, await getSnapshot());
      await input.wallet.commit(handle, {
        cents: cost.actualCents,
        actualTokens: chars,
        model,
        capability: Capability.AudioSpeech,
      });
      return chars;
    },
  };
}

function parseEmbeddingsUsage(
  body: string,
  workUnits: number,
): { promptTokens: number; totalTokens: number } {
  let parsed: unknown;
  try {
    parsed = JSON.parse(body);
  } catch (err) {
    throw new Error(`embeddings billing: invalid JSON response body (${String(err)})`);
  }
  if (!parsed || typeof parsed !== "object") {
    throw new Error("embeddings billing: response body must be an object");
  }
  const usage = (parsed as { usage?: unknown }).usage;
  if (usage && typeof usage === "object") {
    const promptTokens = asNumber((usage as { prompt_tokens?: unknown }).prompt_tokens);
    const totalTokens = asNumber((usage as { total_tokens?: unknown }).total_tokens);
    if (promptTokens !== null && totalTokens !== null) {
      return { promptTokens, totalTokens };
    }
  }
  if (workUnits > 0) {
    return { promptTokens: workUnits, totalTokens: workUnits };
  }
  throw new Error("embeddings billing: response missing usage");
}

function parseImagesCount(body: string): number {
  let parsed: unknown;
  try {
    parsed = JSON.parse(body);
  } catch (err) {
    throw new Error(`images billing: invalid JSON response body (${String(err)})`);
  }
  if (!parsed || typeof parsed !== "object") {
    throw new Error("images billing: response body must be an object");
  }
  const data = (parsed as { data?: unknown }).data;
  if (!Array.isArray(data)) throw new Error("images billing: response missing data array");
  return data.length;
}

function normalizeSpeechInput(input: unknown): string {
  if (typeof input === "string") return input;
  return "";
}

function normalizeEmbeddingInputs(input: unknown): string[] {
  if (typeof input === "string") return [input];
  if (Array.isArray(input)) return input.map(stringifyInput);
  if (input == null) return [""];
  return [stringifyInput(input)];
}

function stringifyInput(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  try {
    return JSON.stringify(value) ?? "";
  } catch {
    return "";
  }
}

function normalizeImageCount(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) && value > 0
    ? Math.floor(value)
    : 1;
}

function normalizeImageSize(value: unknown): string {
  return typeof value === "string" && value.length > 0 ? value : "1024x1024";
}

function normalizeImageQuality(value: unknown): ImageQuality {
  return value === "hd" ? "hd" : "standard";
}

function asCallerTier(value: string): "free" | "prepaid" {
  if (value === "free" || value === "prepaid") return value;
  throw new Error(`non-chat billing: unsupported caller tier ${value}`);
}

function asNumber(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}
