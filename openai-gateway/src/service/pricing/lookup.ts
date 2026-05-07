import { globMatch } from './glob.js';
import type {
  AudioSpeechEntry,
  AudioTranscriptEntry,
  ChatTierEntry,
  EmbeddingsEntry,
  ImageQuality,
  ImagesEntry,
  PricingTier,
  RateCardSnapshot,
} from './types.js';

export function rateForTier(snapshot: RateCardSnapshot, tier: PricingTier): ChatTierEntry {
  const entry = snapshot.chatTiers.find((e) => e.tier === tier);
  if (!entry) throw new Error(`rate card missing tier price entry: ${tier}`);
  return entry;
}

export function resolveChatTier(snapshot: RateCardSnapshot, model: string): PricingTier | null {
  const exact = snapshot.chatModels.find((e) => !e.isPattern && e.modelOrPattern === model);
  if (exact) return exact.tier;
  const patterns = snapshot.chatModels
    .filter((e) => e.isPattern)
    .sort((a, b) => a.sortOrder - b.sortOrder);
  for (const p of patterns) {
    if (globMatch(p.modelOrPattern, model)) return p.tier;
  }
  return null;
}

export function resolveEmbeddingsRate(
  snapshot: RateCardSnapshot,
  model: string,
): EmbeddingsEntry | null {
  const exact = snapshot.embeddings.find((e) => !e.isPattern && e.modelOrPattern === model);
  if (exact) return exact;
  const patterns = snapshot.embeddings
    .filter((e) => e.isPattern)
    .sort((a, b) => a.sortOrder - b.sortOrder);
  for (const p of patterns) {
    if (globMatch(p.modelOrPattern, model)) return p;
  }
  return null;
}

export function resolveAudioSpeechRate(
  snapshot: RateCardSnapshot,
  model: string,
): AudioSpeechEntry | null {
  const exact = snapshot.audioSpeech.find((e) => !e.isPattern && e.modelOrPattern === model);
  if (exact) return exact;
  const patterns = snapshot.audioSpeech
    .filter((e) => e.isPattern)
    .sort((a, b) => a.sortOrder - b.sortOrder);
  for (const p of patterns) {
    if (globMatch(p.modelOrPattern, model)) return p;
  }
  return null;
}

export function resolveAudioTranscriptRate(
  snapshot: RateCardSnapshot,
  model: string,
): AudioTranscriptEntry | null {
  const exact = snapshot.audioTranscripts.find((e) => !e.isPattern && e.modelOrPattern === model);
  if (exact) return exact;
  const patterns = snapshot.audioTranscripts
    .filter((e) => e.isPattern)
    .sort((a, b) => a.sortOrder - b.sortOrder);
  for (const p of patterns) {
    if (globMatch(p.modelOrPattern, model)) return p;
  }
  return null;
}

export function resolveImagesRate(
  snapshot: RateCardSnapshot,
  model: string,
  size: string,
  quality: ImageQuality,
): ImagesEntry | null {
  const exact = snapshot.images.find(
    (e) => !e.isPattern && e.modelOrPattern === model && e.size === size && e.quality === quality,
  );
  if (exact) return exact;
  const patterns = snapshot.images
    .filter((e) => e.isPattern)
    .sort((a, b) => a.sortOrder - b.sortOrder);
  for (const p of patterns) {
    if (p.size !== size || p.quality !== quality) continue;
    if (globMatch(p.modelOrPattern, model)) return p;
  }
  return null;
}
