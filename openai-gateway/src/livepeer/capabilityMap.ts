export const Capability = {
  ChatCompletions: 'openai:chat-completions',
  Embeddings: 'openai:embeddings',
  AudioTranscriptions: 'openai:audio-transcriptions',
  AudioSpeech: 'openai:audio-speech',
  ImagesGenerations: 'openai:images-generations',
  Realtime: 'openai:realtime',
} as const;

export type CapabilityId = (typeof Capability)[keyof typeof Capability];

export const LegacyCapabilityAlias = {
  'openai:/v1/chat/completions': Capability.ChatCompletions,
  'openai:/v1/embeddings': Capability.Embeddings,
  'openai:/v1/audio/transcriptions': Capability.AudioTranscriptions,
  'openai:/v1/audio/speech': Capability.AudioSpeech,
  'openai:/v1/images/generations': Capability.ImagesGenerations,
  'openai:/v1/realtime': Capability.Realtime,
} as const satisfies Record<string, CapabilityId>;

export function normalizeCapabilityId(value: string): string {
  return LegacyCapabilityAlias[value as keyof typeof LegacyCapabilityAlias] ?? value;
}
