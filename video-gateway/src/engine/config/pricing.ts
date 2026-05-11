import type { Capability, Codec, Resolution } from "../types/index.js";

export interface PricingConfig {
  vodCentsPerSecond: Record<Resolution, Record<Codec, number>>;
  liveCentsPerSecond: number;
  overheadCents: number;
}

export function defaultPricingConfig(): PricingConfig {
  return {
    vodCentsPerSecond: {
      "240p": { h264: 0.001, hevc: 0.0012, av1: 0.0015 },
      "360p": { h264: 0.002, hevc: 0.0024, av1: 0.003 },
      "480p": { h264: 0.003, hevc: 0.0036, av1: 0.0045 },
      "720p": { h264: 0.005, hevc: 0.006, av1: 0.0075 },
      "1080p": { h264: 0.008, hevc: 0.0096, av1: 0.012 },
      "2160p": { h264: 0.02, hevc: 0.024, av1: 0.03 },
    },
    liveCentsPerSecond: 0.05,
    overheadCents: 0,
  };
}

export type { Capability };
