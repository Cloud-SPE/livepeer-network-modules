export type PricingTier = 'starter' | 'standard' | 'pro' | 'premium';

export interface ChatTierEntry {
  tier: PricingTier;
  inputUsdPerMillion: number;
  outputUsdPerMillion: number;
}

export interface ChatModelEntry {
  modelOrPattern: string;
  isPattern: boolean;
  tier: PricingTier;
  sortOrder: number;
}

export interface EmbeddingsEntry {
  modelOrPattern: string;
  isPattern: boolean;
  usdPerMillionTokens: number;
  sortOrder: number;
}

export interface AudioSpeechEntry {
  modelOrPattern: string;
  isPattern: boolean;
  usdPerMillionChars: number;
  sortOrder: number;
}

export interface AudioTranscriptEntry {
  modelOrPattern: string;
  isPattern: boolean;
  usdPerMinute: number;
  sortOrder: number;
}

export interface RateCardSnapshot {
  chatTiers: ChatTierEntry[];
  chatModels: ChatModelEntry[];
  embeddings: EmbeddingsEntry[];
  audioSpeech: AudioSpeechEntry[];
  audioTranscripts: AudioTranscriptEntry[];
}
