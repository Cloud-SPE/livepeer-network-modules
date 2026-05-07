// Per-domain config helpers for vtuber-specific knobs.
// Source: `livepeer-network-suite/livepeer-vtuber-gateway/src/config/vtuber.ts`.

export interface VtuberSessionLimits {
  defaultTtlSeconds: number;
  bearerTtlSeconds: number;
  defaultOffering: string;
  workerCallTimeoutMs: number;
  relayMaxPerSession: number;
}

export const DEFAULT_VTUBER_SESSION_LIMITS: VtuberSessionLimits = {
  defaultTtlSeconds: 3600,
  bearerTtlSeconds: 7200,
  defaultOffering: "default",
  workerCallTimeoutMs: 15_000,
  relayMaxPerSession: 8,
};
