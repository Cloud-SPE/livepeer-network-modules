import type { Codec, EncodingTier, RenditionSpec, Resolution } from "./caller.js";

export interface Asset {
  id: string;
  projectId: string;
  status: AssetStatus;
  sourceType: AssetSourceType;
  selectedOffering?: string;
  sourceUrl?: string;
  durationSec?: number;
  width?: number;
  height?: number;
  frameRate?: number;
  audioCodec?: string;
  videoCodec?: string;
  encodingTier: EncodingTier;
  ffprobeJson?: unknown;
  errorMessage?: string;
  createdAt: Date;
  readyAt?: Date;
  deletedAt?: Date;
}

export type AssetStatus = "preparing" | "ready" | "errored" | "deleted";
export type AssetSourceType = "upload" | "live_recording" | "imported";

export interface Rendition {
  id: string;
  assetId: string;
  resolution: Resolution;
  codec: Codec;
  bitrateKbps: number;
  storageKey?: string;
  status: RenditionStatus;
  durationSec?: number;
  createdAt: Date;
  completedAt?: Date;
}

export type RenditionStatus = "queued" | "running" | "completed" | "failed";

export interface ProbeResult {
  durationSec: number;
  width: number;
  height: number;
  frameRate: number;
  audioCodec: string;
  videoCodec: string;
  raw: unknown;
}

export type { Codec, EncodingTier, RenditionSpec, Resolution };
