import type pg from 'pg';

import type { PricingTier, RateCardSnapshot } from '../service/pricing/types.js';

export type Queryable = Pick<pg.Pool, 'query'>;

interface ChatTierRow {
  tier: string;
  input_usd_per_million: string;
  output_usd_per_million: string;
}
interface ChatModelRow {
  model_or_pattern: string;
  is_pattern: boolean;
  tier: string;
  sort_order: number;
}
interface EmbeddingsRow {
  model_or_pattern: string;
  is_pattern: boolean;
  usd_per_million_tokens: string;
  sort_order: number;
}
interface AudioSpeechRow {
  model_or_pattern: string;
  is_pattern: boolean;
  usd_per_million_chars: string;
  sort_order: number;
}
interface AudioTranscriptRow {
  model_or_pattern: string;
  is_pattern: boolean;
  usd_per_minute: string;
  sort_order: number;
}

function asPricingTier(s: string): PricingTier {
  if (s === 'starter' || s === 'standard' || s === 'pro' || s === 'premium') return s;
  throw new Error(`unexpected pricing tier in DB: ${s}`);
}

export async function loadRateCardSnapshot(pool: Queryable): Promise<RateCardSnapshot> {
  const tierRes = await pool.query<ChatTierRow>(
    'SELECT tier, input_usd_per_million::text, output_usd_per_million::text FROM app.rate_card_chat_tiers',
  );
  const chatModelRes = await pool.query<ChatModelRow>(
    'SELECT model_or_pattern, is_pattern, tier, sort_order FROM app.rate_card_chat_models',
  );
  const embRes = await pool.query<EmbeddingsRow>(
    'SELECT model_or_pattern, is_pattern, usd_per_million_tokens::text, sort_order FROM app.rate_card_embeddings',
  );
  const speechRes = await pool.query<AudioSpeechRow>(
    'SELECT model_or_pattern, is_pattern, usd_per_million_chars::text, sort_order FROM app.rate_card_audio_speech',
  );
  const transcriptRes = await pool.query<AudioTranscriptRow>(
    'SELECT model_or_pattern, is_pattern, usd_per_minute::text, sort_order FROM app.rate_card_audio_transcripts',
  );

  return {
    chatTiers: tierRes.rows.map((r) => ({
      tier: asPricingTier(r.tier),
      inputUsdPerMillion: Number(r.input_usd_per_million),
      outputUsdPerMillion: Number(r.output_usd_per_million),
    })),
    chatModels: chatModelRes.rows.map((r) => ({
      modelOrPattern: r.model_or_pattern,
      isPattern: r.is_pattern,
      tier: asPricingTier(r.tier),
      sortOrder: r.sort_order,
    })),
    embeddings: embRes.rows.map((r) => ({
      modelOrPattern: r.model_or_pattern,
      isPattern: r.is_pattern,
      usdPerMillionTokens: Number(r.usd_per_million_tokens),
      sortOrder: r.sort_order,
    })),
    audioSpeech: speechRes.rows.map((r) => ({
      modelOrPattern: r.model_or_pattern,
      isPattern: r.is_pattern,
      usdPerMillionChars: Number(r.usd_per_million_chars),
      sortOrder: r.sort_order,
    })),
    audioTranscripts: transcriptRes.rows.map((r) => ({
      modelOrPattern: r.model_or_pattern,
      isPattern: r.is_pattern,
      usdPerMinute: Number(r.usd_per_minute),
      sortOrder: r.sort_order,
    })),
  };
}
