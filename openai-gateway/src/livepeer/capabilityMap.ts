export const Capability = {
  ChatCompletions: 'openai:/v1/chat/completions',
  Embeddings: 'openai:/v1/embeddings',
  AudioTranscriptions: 'openai:/v1/audio/transcriptions',
  AudioSpeech: 'openai:/v1/audio/speech',
  ImagesGenerations: 'openai:/v1/images/generations',
  Realtime: 'openai:/v1/realtime',
} as const;

export type CapabilityId = (typeof Capability)[keyof typeof Capability];
