export interface Caller {
  id: string;
  tier: string;
  metadata?: unknown;
}

export type Capability =
  | "video:transcode.vod"
  | "video:transcode.abr"
  | "video:live.rtmp";

export type Codec = "h264" | "hevc" | "av1";

export type Resolution = "240p" | "360p" | "480p" | "720p" | "1080p";

export interface RenditionSpec {
  resolution: Resolution;
  bitrateKbps: number;
  codec: Codec;
}

export type EncodingTier = "baseline" | "standard" | "premium";

export interface CostQuote {
  cents: number;
  wei: bigint;
  estimatedSeconds: number | null;
  renditions: RenditionSpec[];
  callerTier: string;
  capability: Capability;
}

export interface UsageReport {
  cents: number;
  wei: bigint;
  actualSeconds: number;
  renditions: RenditionSpec[];
  capability: Capability;
}

export type ReservationHandle = unknown;
