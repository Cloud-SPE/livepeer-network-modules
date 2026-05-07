import type { billing } from '@livepeer-rewrite/customer-portal';

import { loadRateCardSnapshot, type Queryable } from '../../repo/rateCard.js';
import {
  resolveAudioSpeechRate,
  resolveAudioTranscriptRate,
  resolveChatTier,
  resolveEmbeddingsRate,
  rateForTier,
} from './lookup.js';
import type { RateCardSnapshot } from './types.js';

type RateCardResolver = billing.RateCardResolver;
type ResolveResult = Awaited<ReturnType<RateCardResolver['resolve']>>;

export interface CreateRateCardResolverInput {
  pool: Queryable;
  cacheTtlMs?: number;
  now?: () => number;
}

export function createRateCardResolver(
  input: CreateRateCardResolverInput,
): RateCardResolver {
  const ttl = input.cacheTtlMs ?? 30_000;
  const now = input.now ?? Date.now;
  let cached: { snapshot: RateCardSnapshot; loadedAt: number } | null = null;

  async function getSnapshot(): Promise<RateCardSnapshot> {
    if (cached && now() - cached.loadedAt < ttl) return cached.snapshot;
    const snapshot = await loadRateCardSnapshot(input.pool);
    cached = { snapshot, loadedAt: now() };
    return snapshot;
  }

  return {
    async resolve(req): Promise<ResolveResult> {
      const snapshot = await getSnapshot();
      const result = resolveCapability(snapshot, req.capability, req.offering);
      return result;
    },
  };
}

function resolveCapability(
  snapshot: RateCardSnapshot,
  capability: string,
  offering: string,
): ResolveResult {
  const model = offering;
  switch (capability) {
    case 'openai:/v1/chat/completions': {
      const tier = resolveChatTier(snapshot, model);
      if (!tier) return null;
      const rate = rateForTier(snapshot, tier);
      return {
        usdPerUnit: usdToMicroCents(rate.inputUsdPerMillion),
        unit: 'million_input_tokens',
      };
    }
    case 'openai:/v1/embeddings': {
      const rate = resolveEmbeddingsRate(snapshot, model);
      if (!rate) return null;
      return {
        usdPerUnit: usdToMicroCents(rate.usdPerMillionTokens),
        unit: 'million_tokens',
      };
    }
    case 'openai:/v1/audio/speech': {
      const rate = resolveAudioSpeechRate(snapshot, model);
      if (!rate) return null;
      return {
        usdPerUnit: usdToMicroCents(rate.usdPerMillionChars),
        unit: 'million_chars',
      };
    }
    case 'openai:/v1/audio/transcriptions': {
      const rate = resolveAudioTranscriptRate(snapshot, model);
      if (!rate) return null;
      return {
        usdPerUnit: usdToMicroCents(rate.usdPerMinute),
        unit: 'minute',
      };
    }
    default:
      return null;
  }
}

function usdToMicroCents(usd: number): bigint {
  return BigInt(Math.round(usd * 100 * 10_000));
}
