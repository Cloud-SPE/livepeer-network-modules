import type pg from 'pg';

import type { ImageQuality, PricingTier, RateCardSnapshot } from '../service/pricing/types.js';

export type Queryable = Pick<pg.Pool, 'query'>;
type TransactionalClient = {
  query: (sql: string, args?: unknown[]) => Promise<unknown>;
  release: () => void;
};
type Transactional = {
  connect: () => Promise<TransactionalClient>;
};

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
interface ImagesRow {
  model_or_pattern: string;
  is_pattern: boolean;
  size: string;
  quality: string;
  usd_per_image: string;
  sort_order: number;
}

function asImageQuality(s: string): ImageQuality {
  if (s === 'standard' || s === 'hd') return s;
  throw new Error(`unexpected image quality in DB: ${s}`);
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
  const imageRes = await pool.query<ImagesRow>(
    'SELECT model_or_pattern, is_pattern, size, quality, usd_per_image::text, sort_order FROM app.rate_card_images',
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
    images: imageRes.rows.map((r) => ({
      modelOrPattern: r.model_or_pattern,
      isPattern: r.is_pattern,
      size: r.size,
      quality: asImageQuality(r.quality),
      usdPerImage: Number(r.usd_per_image),
      sortOrder: r.sort_order,
    })),
  };
}

export async function replaceRateCardSnapshot(
  pool: Queryable & Partial<Transactional>,
  snapshot: RateCardSnapshot,
): Promise<void> {
  if (!('connect' in pool) || typeof pool.connect !== 'function') {
    throw new Error('replaceRateCardSnapshot requires a pg.Pool-like client with connect()');
  }
  const client = await pool.connect();
  try {
    await client.query('BEGIN');
    await client.query('DELETE FROM app.rate_card_chat_models');
    await client.query('DELETE FROM app.rate_card_chat_tiers');
    await client.query('DELETE FROM app.rate_card_embeddings');
    await client.query('DELETE FROM app.rate_card_audio_speech');
    await client.query('DELETE FROM app.rate_card_audio_transcripts');
    await client.query('DELETE FROM app.rate_card_images');

    for (const row of snapshot.chatTiers) {
      await client.query(
        `INSERT INTO app.rate_card_chat_tiers (tier, input_usd_per_million, output_usd_per_million)
         VALUES ($1, $2, $3)`,
        [row.tier, row.inputUsdPerMillion, row.outputUsdPerMillion],
      );
    }
    for (const row of snapshot.chatModels) {
      await client.query(
        `INSERT INTO app.rate_card_chat_models (model_or_pattern, is_pattern, tier, sort_order)
         VALUES ($1, $2, $3, $4)`,
        [row.modelOrPattern, row.isPattern, row.tier, row.sortOrder],
      );
    }
    for (const row of snapshot.embeddings) {
      await client.query(
        `INSERT INTO app.rate_card_embeddings (model_or_pattern, is_pattern, usd_per_million_tokens, sort_order)
         VALUES ($1, $2, $3, $4)`,
        [row.modelOrPattern, row.isPattern, row.usdPerMillionTokens, row.sortOrder],
      );
    }
    for (const row of snapshot.audioSpeech) {
      await client.query(
        `INSERT INTO app.rate_card_audio_speech (model_or_pattern, is_pattern, usd_per_million_chars, sort_order)
         VALUES ($1, $2, $3, $4)`,
        [row.modelOrPattern, row.isPattern, row.usdPerMillionChars, row.sortOrder],
      );
    }
    for (const row of snapshot.audioTranscripts) {
      await client.query(
        `INSERT INTO app.rate_card_audio_transcripts (model_or_pattern, is_pattern, usd_per_minute, sort_order)
         VALUES ($1, $2, $3, $4)`,
        [row.modelOrPattern, row.isPattern, row.usdPerMinute, row.sortOrder],
      );
    }
    for (const row of snapshot.images) {
      await client.query(
        `INSERT INTO app.rate_card_images (model_or_pattern, is_pattern, size, quality, usd_per_image, sort_order)
         VALUES ($1, $2, $3, $4, $5, $6)`,
        [row.modelOrPattern, row.isPattern, row.size, row.quality, row.usdPerImage, row.sortOrder],
      );
    }

    await client.query('COMMIT');
  } catch (err) {
    await client.query('ROLLBACK');
    throw err;
  } finally {
    client.release();
  }
}
