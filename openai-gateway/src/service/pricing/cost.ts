import {
  rateForTier,
  resolveAudioSpeechRate,
  resolveAudioTranscriptRate,
  resolveChatTier,
  resolveEmbeddingsRate,
} from './lookup.js';
import type { PricingTier, RateCardSnapshot } from './types.js';

const MILLION = 1_000_000n;

export class ModelNotFoundError extends Error {
  constructor(model: string) {
    super(`model not in rate card: ${model}`);
    this.name = 'ModelNotFoundError';
  }
}

function resolveTierOrThrow(snapshot: RateCardSnapshot, model: string): PricingTier {
  const tier = resolveChatTier(snapshot, model);
  if (!tier) throw new ModelNotFoundError(model);
  return tier;
}

export interface ChatReservationEstimate {
  estCents: bigint;
  promptEstimateTokens: number;
  maxCompletionTokens: number;
  pricingTier: PricingTier;
}

export function estimateChatReservation(
  promptText: string,
  maxCompletionTokens: number,
  model: string,
  snapshot: RateCardSnapshot,
): ChatReservationEstimate {
  const pricingTier = resolveTierOrThrow(snapshot, model);
  const rate = rateForTier(snapshot, pricingTier);
  const promptEstimateTokens = Math.max(1, Math.ceil(promptText.length / 3));
  const estCents = computeChatCostCents(
    BigInt(promptEstimateTokens),
    BigInt(maxCompletionTokens),
    rate.inputUsdPerMillion,
    rate.outputUsdPerMillion,
  );
  return { estCents, promptEstimateTokens, maxCompletionTokens, pricingTier };
}

export interface ChatActualCost {
  actualCents: bigint;
  pricingTier: PricingTier;
}

export function computeChatActualCost(
  promptTokens: number,
  completionTokens: number,
  model: string,
  snapshot: RateCardSnapshot,
): ChatActualCost {
  const pricingTier = resolveTierOrThrow(snapshot, model);
  const rate = rateForTier(snapshot, pricingTier);
  const actualCents = computeChatCostCents(
    BigInt(promptTokens),
    BigInt(completionTokens),
    rate.inputUsdPerMillion,
    rate.outputUsdPerMillion,
  );
  return { actualCents, pricingTier };
}

function computeChatCostCents(
  promptTokens: bigint,
  outputTokens: bigint,
  inputUsdPerMillion: number,
  outputUsdPerMillion: number,
): bigint {
  const inputCentsPerMillion = BigInt(Math.round(inputUsdPerMillion * 100 * 10_000));
  const outputCentsPerMillion = BigInt(Math.round(outputUsdPerMillion * 100 * 10_000));
  const microPerMillion =
    promptTokens * inputCentsPerMillion + outputTokens * outputCentsPerMillion;
  const denom = MILLION * 10_000n;
  return (microPerMillion + denom - 1n) / denom;
}

export function estimateEmbeddingsReservation(
  inputs: string[],
  model: string,
  snapshot: RateCardSnapshot,
): { estCents: bigint; promptEstimateTokens: number } {
  const rate = resolveEmbeddingsRate(snapshot, model);
  if (!rate) throw new ModelNotFoundError(model);
  const promptEstimateTokens = Math.max(
    1,
    Math.ceil(inputs.reduce((sum, s) => sum + s.length, 0) / 3),
  );
  const estCents = computeInputOnlyCostCents(
    BigInt(promptEstimateTokens),
    rate.usdPerMillionTokens,
  );
  return { estCents, promptEstimateTokens };
}

export function computeEmbeddingsActualCost(
  promptTokens: number,
  model: string,
  snapshot: RateCardSnapshot,
): { actualCents: bigint } {
  const rate = resolveEmbeddingsRate(snapshot, model);
  if (!rate) throw new ModelNotFoundError(model);
  const actualCents = computeInputOnlyCostCents(BigInt(promptTokens), rate.usdPerMillionTokens);
  return { actualCents };
}

function computeInputOnlyCostCents(
  promptTokens: bigint,
  inputUsdPerMillion: number,
): bigint {
  const inputCentsPerMillion = BigInt(Math.round(inputUsdPerMillion * 100 * 10_000));
  const inputMicro = (promptTokens * inputCentsPerMillion) / MILLION;
  return (inputMicro + 9999n) / 10_000n;
}

export function estimateAudioSpeechReservation(
  inputCharCount: number,
  model: string,
  snapshot: RateCardSnapshot,
): { estCents: bigint; charCount: number } {
  const rate = resolveAudioSpeechRate(snapshot, model);
  if (!rate) throw new ModelNotFoundError(model);
  const charCount = Math.max(0, inputCharCount);
  const estCents = computePerCharCents(BigInt(charCount), rate.usdPerMillionChars);
  return { estCents, charCount };
}

export function computeAudioSpeechActualCost(
  charsBilled: number,
  model: string,
  snapshot: RateCardSnapshot,
): { actualCents: bigint } {
  const rate = resolveAudioSpeechRate(snapshot, model);
  if (!rate) throw new ModelNotFoundError(model);
  const actualCents = computePerCharCents(
    BigInt(Math.max(0, charsBilled)),
    rate.usdPerMillionChars,
  );
  return { actualCents };
}

function computePerCharCents(chars: bigint, usdPerMillion: number): bigint {
  const centsPerMillion = BigInt(Math.round(usdPerMillion * 100 * 10_000));
  const micro = (chars * centsPerMillion) / MILLION;
  return (micro + 9999n) / 10_000n;
}

const TRANSCRIPTIONS_BITRATE_BYTES_PER_SEC = 8_000;
const TRANSCRIPTIONS_MAX_RESERVE_SECONDS = 60 * 60;

export function estimateAudioTranscriptReservation(
  fileSizeBytes: number,
  model: string,
  snapshot: RateCardSnapshot,
): { estCents: bigint; estimatedSeconds: number } {
  const rate = resolveAudioTranscriptRate(snapshot, model);
  if (!rate) throw new ModelNotFoundError(model);
  const raw = Math.ceil(Math.max(0, fileSizeBytes) / TRANSCRIPTIONS_BITRATE_BYTES_PER_SEC);
  const estimatedSeconds = Math.max(1, Math.min(raw, TRANSCRIPTIONS_MAX_RESERVE_SECONDS));
  const estCents = computePerSecondCents(estimatedSeconds, rate.usdPerMinute);
  return { estCents, estimatedSeconds };
}

export function computeAudioTranscriptActualCost(
  reportedSeconds: number,
  model: string,
  snapshot: RateCardSnapshot,
): { actualCents: bigint } {
  const rate = resolveAudioTranscriptRate(snapshot, model);
  if (!rate) throw new ModelNotFoundError(model);
  const seconds = Math.max(0, Math.ceil(reportedSeconds));
  const actualCents = computePerSecondCents(seconds, rate.usdPerMinute);
  return { actualCents };
}

function computePerSecondCents(seconds: number, usdPerMinute: number): bigint {
  const microCentsPerSecond = BigInt(Math.round((usdPerMinute * 100 * 10_000) / 60));
  const micro = BigInt(Math.max(0, seconds)) * microCentsPerSecond;
  return (micro + 9999n) / 10_000n;
}
