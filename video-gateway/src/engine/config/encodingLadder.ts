import type { Codec, EncodingTier, RenditionSpec, Resolution } from "../types/index.js";

export const RESOLUTIONS_ASCENDING: Resolution[] = [
  "240p",
  "360p",
  "480p",
  "720p",
  "1080p",
];

export const DEFAULT_BITRATE_KBPS: Record<Resolution, Record<Codec, number>> = {
  "240p": { h264: 400, hevc: 280, av1: 220 },
  "360p": { h264: 800, hevc: 560, av1: 440 },
  "480p": { h264: 1400, hevc: 980, av1: 770 },
  "720p": { h264: 2800, hevc: 1960, av1: 1540 },
  "1080p": { h264: 5000, hevc: 3500, av1: 2750 },
};

export interface EncodingLadder {
  resolutions: Resolution[];
  bitrateKbps: Record<Resolution, Record<Codec, number>>;
}

export function defaultEncodingLadder(): EncodingLadder {
  return {
    resolutions: [...RESOLUTIONS_ASCENDING],
    bitrateKbps: structuredClone(DEFAULT_BITRATE_KBPS),
  };
}

const TIER_CODECS: Record<EncodingTier, Codec[]> = {
  baseline: ["h264"],
  standard: ["h264", "hevc"],
  premium: ["h264", "hevc", "av1"],
};

export function expandTier(
  tier: EncodingTier,
  ladder: EncodingLadder = defaultEncodingLadder(),
): RenditionSpec[] {
  const codecs = TIER_CODECS[tier];
  const out: RenditionSpec[] = [];
  for (const res of ladder.resolutions) {
    for (const codec of codecs) {
      const bitrate = ladder.bitrateKbps[res]?.[codec];
      if (bitrate === undefined) continue;
      out.push({ resolution: res, bitrateKbps: bitrate, codec });
    }
  }
  return out;
}
