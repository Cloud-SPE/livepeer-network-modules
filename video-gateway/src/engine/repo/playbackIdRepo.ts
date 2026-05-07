import type { PlaybackId } from "../types/index.js";

export interface PlaybackIdRepo {
  insert(pb: Omit<PlaybackId, "createdAt"> & { createdAt?: Date }): Promise<PlaybackId>;
  byId(id: string): Promise<PlaybackId | null>;
  byAsset(assetId: string): Promise<PlaybackId[]>;
  byLiveStream(liveStreamId: string): Promise<PlaybackId[]>;
  delete(id: string): Promise<void>;
}
