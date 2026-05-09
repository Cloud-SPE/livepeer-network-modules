import test from 'node:test';
import assert from 'node:assert/strict';

import {
  globMatch,
  resolveAudioSpeechRate,
  resolveAudioTranscriptRate,
  resolveChatTier,
  resolveEmbeddingsRate,
  resolveImagesRate,
  rateForTier,
  estimateChatReservation,
  computeChatActualCost,
  estimateEmbeddingsReservation,
  estimateAudioSpeechReservation,
  estimateAudioTranscriptReservation,
  estimateImagesReservation,
  ModelNotFoundError,
  createRateCardResolver,
} from '../src/service/pricing/index.js';
import type { RateCardSnapshot } from '../src/service/pricing/types.js';
import type { Queryable } from '../src/repo/rateCard.js';

const fixture: RateCardSnapshot = {
  chatTiers: [
    { tier: 'starter', inputUsdPerMillion: 0.05, outputUsdPerMillion: 0.1 },
    { tier: 'standard', inputUsdPerMillion: 0.15, outputUsdPerMillion: 0.4 },
    { tier: 'pro', inputUsdPerMillion: 0.4, outputUsdPerMillion: 1.2 },
    { tier: 'premium', inputUsdPerMillion: 2.5, outputUsdPerMillion: 6 },
  ],
  chatModels: [
    { modelOrPattern: 'model-small', isPattern: false, tier: 'starter', sortOrder: 100 },
    { modelOrPattern: 'model-medium', isPattern: false, tier: 'standard', sortOrder: 100 },
    { modelOrPattern: 'Qwen3.*', isPattern: true, tier: 'pro', sortOrder: 50 },
    { modelOrPattern: '*', isPattern: true, tier: 'starter', sortOrder: 1000 },
  ],
  embeddings: [
    { modelOrPattern: 'text-embedding-3-small', isPattern: false, usdPerMillionTokens: 0.005, sortOrder: 100 },
    { modelOrPattern: 'bge-*', isPattern: true, usdPerMillionTokens: 0.002, sortOrder: 100 },
  ],
  audioSpeech: [
    { modelOrPattern: 'tts-1', isPattern: false, usdPerMillionChars: 5, sortOrder: 100 },
  ],
  audioTranscripts: [
    { modelOrPattern: 'whisper-1', isPattern: false, usdPerMinute: 0.003, sortOrder: 100 },
  ],
  images: [
    { modelOrPattern: 'sdxl', isPattern: false, size: '1024x1024', quality: 'standard', usdPerImage: 0.002, sortOrder: 100 },
    { modelOrPattern: 'sdxl', isPattern: false, size: '1024x1024', quality: 'hd', usdPerImage: 0.005, sortOrder: 100 },
    { modelOrPattern: 'realvis-*', isPattern: true, size: '1024x1024', quality: 'standard', usdPerImage: 0.001, sortOrder: 100 },
  ],
};

test('globMatch handles * and ? wildcards', () => {
  assert.equal(globMatch('Qwen3.*', 'Qwen3.6-27B'), true);
  assert.equal(globMatch('Qwen3.*', 'Qwen2-7B'), false);
  assert.equal(globMatch('?qwen*', 'Aqwen-7B'), true);
  assert.equal(globMatch('?qwen*', 'qwen-7B'), false);
  assert.equal(globMatch('*-27B', 'Llama-27B'), true);
});

test('resolveChatTier hits exact match before any pattern', () => {
  assert.equal(resolveChatTier(fixture, 'model-small'), 'starter');
});

test('resolveChatTier falls back to lowest sort_order pattern', () => {
  assert.equal(resolveChatTier(fixture, 'Qwen3.6-27B'), 'pro');
  assert.equal(resolveChatTier(fixture, 'unknown-model'), 'starter');
});

test('resolveChatTier returns null when no match (no fallback patterns)', () => {
  const noFallback: RateCardSnapshot = {
    ...fixture,
    chatModels: fixture.chatModels.filter((e) => e.modelOrPattern !== '*'),
  };
  assert.equal(resolveChatTier(noFallback, 'random-thing'), null);
});

test('rateForTier returns the tier price entry', () => {
  const rate = rateForTier(fixture, 'pro');
  assert.equal(rate.inputUsdPerMillion, 0.4);
  assert.equal(rate.outputUsdPerMillion, 1.2);
});

test('estimateChatReservation computes a non-zero cost for a known model', () => {
  const r = estimateChatReservation('Hello world', 100, 'model-small', fixture);
  assert.equal(r.pricingTier, 'starter');
  assert.ok(r.estCents >= 0n);
  assert.equal(r.maxCompletionTokens, 100);
});

test('estimateChatReservation throws ModelNotFoundError for unmatched model in a no-fallback card', () => {
  const noFallback: RateCardSnapshot = {
    ...fixture,
    chatModels: [],
  };
  assert.throws(() => estimateChatReservation('hi', 50, 'no-such-model', noFallback), ModelNotFoundError);
});

test('computeChatActualCost rounds up sub-cent amounts', () => {
  const cost = computeChatActualCost(1, 1, 'model-small', fixture);
  assert.ok(cost.actualCents >= 1n);
});

test('resolveEmbeddingsRate honours globs', () => {
  const exact = resolveEmbeddingsRate(fixture, 'text-embedding-3-small');
  assert.equal(exact?.usdPerMillionTokens, 0.005);

  const pattern = resolveEmbeddingsRate(fixture, 'bge-large');
  assert.equal(pattern?.usdPerMillionTokens, 0.002);

  assert.equal(resolveEmbeddingsRate(fixture, 'never-heard-of-it'), null);
});

test('estimateEmbeddingsReservation costs a one-line input', () => {
  const r = estimateEmbeddingsReservation(['hello'], 'text-embedding-3-small', fixture);
  assert.ok(r.promptEstimateTokens >= 1);
  assert.ok(r.estCents >= 0n);
});

test('resolveAudioSpeechRate finds tts-1 exactly', () => {
  assert.equal(resolveAudioSpeechRate(fixture, 'tts-1')?.usdPerMillionChars, 5);
});

test('estimateAudioSpeechReservation computes per-million-char cost', () => {
  const r = estimateAudioSpeechReservation(2_000_000, 'tts-1', fixture);
  assert.equal(r.charCount, 2_000_000);
  assert.ok(r.estCents > 0n);
});

test('resolveAudioTranscriptRate finds whisper-1 exactly', () => {
  assert.equal(resolveAudioTranscriptRate(fixture, 'whisper-1')?.usdPerMinute, 0.003);
});

test('estimateAudioTranscriptReservation rounds up to at least one second', () => {
  const r = estimateAudioTranscriptReservation(0, 'whisper-1', fixture);
  assert.equal(r.estimatedSeconds, 1);
});

test('resolveImagesRate hits exact + glob and respects size/quality', () => {
  assert.equal(resolveImagesRate(fixture, 'sdxl', '1024x1024', 'standard')?.usdPerImage, 0.002);
  assert.equal(resolveImagesRate(fixture, 'sdxl', '1024x1024', 'hd')?.usdPerImage, 0.005);
  assert.equal(resolveImagesRate(fixture, 'realvis-xl', '1024x1024', 'standard')?.usdPerImage, 0.001);
  assert.equal(resolveImagesRate(fixture, 'realvis-xl', '1024x1024', 'hd'), null);
  assert.equal(resolveImagesRate(fixture, 'sdxl', '512x512', 'standard'), null);
});

test('estimateImagesReservation multiplies per-image cost by n', () => {
  const r = estimateImagesReservation(3, 'sdxl', '1024x1024', 'standard', fixture);
  assert.equal(r.n, 3);
  assert.equal(r.estCents, r.perImageCents * 3n);
});

test('createRateCardResolver routes capability + offering through the lookup tables', async () => {
  const calls: string[] = [];
  const fakePool: Queryable = {
    query: (async (text: string) => {
      calls.push(text);
      if (text.includes('rate_card_chat_tiers'))
        return { rows: fixture.chatTiers.map((t) => ({
          tier: t.tier,
          input_usd_per_million: String(t.inputUsdPerMillion),
          output_usd_per_million: String(t.outputUsdPerMillion),
        })) };
      if (text.includes('rate_card_chat_models'))
        return { rows: fixture.chatModels.map((m) => ({
          model_or_pattern: m.modelOrPattern,
          is_pattern: m.isPattern,
          tier: m.tier,
          sort_order: m.sortOrder,
        })) };
      if (text.includes('rate_card_embeddings'))
        return { rows: fixture.embeddings.map((e) => ({
          model_or_pattern: e.modelOrPattern,
          is_pattern: e.isPattern,
          usd_per_million_tokens: String(e.usdPerMillionTokens),
          sort_order: e.sortOrder,
        })) };
      if (text.includes('rate_card_audio_speech'))
        return { rows: fixture.audioSpeech.map((s) => ({
          model_or_pattern: s.modelOrPattern,
          is_pattern: s.isPattern,
          usd_per_million_chars: String(s.usdPerMillionChars),
          sort_order: s.sortOrder,
        })) };
      if (text.includes('rate_card_audio_transcripts'))
        return { rows: fixture.audioTranscripts.map((t) => ({
          model_or_pattern: t.modelOrPattern,
          is_pattern: t.isPattern,
          usd_per_minute: String(t.usdPerMinute),
          sort_order: t.sortOrder,
        })) };
      if (text.includes('rate_card_images'))
        return { rows: fixture.images.map((i) => ({
          model_or_pattern: i.modelOrPattern,
          is_pattern: i.isPattern,
          size: i.size,
          quality: i.quality,
          usd_per_image: String(i.usdPerImage),
          sort_order: i.sortOrder,
        })) };
      return { rows: [] };
    }) as unknown as Queryable['query'],
  };
  const resolver = createRateCardResolver({ pool: fakePool });

  const chat = await resolver.resolve({ capability: 'openai:chat-completions', offering: 'model-small' });
  assert.ok(chat, 'chat resolution returned null');
  assert.equal(chat.unit, 'million_input_tokens');

  const emb = await resolver.resolve({ capability: 'openai:embeddings', offering: 'text-embedding-3-small' });
  assert.ok(emb, 'embeddings resolution returned null');
  assert.equal(emb.unit, 'million_tokens');

  const speech = await resolver.resolve({ capability: 'openai:audio-speech', offering: 'tts-1' });
  assert.equal(speech?.unit, 'million_chars');

  const txn = await resolver.resolve({ capability: 'openai:audio-transcriptions', offering: 'whisper-1' });
  assert.equal(txn?.unit, 'minute');

  const img = await resolver.resolve({ capability: 'openai:images-generations', offering: 'sdxl' });
  assert.equal(img?.unit, 'image');

  const miss = await resolver.resolve({ capability: 'openai:embeddings', offering: 'no-such' });
  assert.equal(miss, null);

  const unknownCap = await resolver.resolve({ capability: 'unknown', offering: 'tts-1' });
  assert.equal(unknownCap, null);
});
