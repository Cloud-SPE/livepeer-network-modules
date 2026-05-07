import type { Asset, AssetStatus } from "../types/index.js";

export interface AssetRepo {
  insert(asset: Omit<Asset, "createdAt"> & { createdAt?: Date }): Promise<Asset>;
  byId(id: string): Promise<Asset | null>;
  byPlaybackId(playbackId: string): Promise<Asset | null>;
  updateStatus(
    id: string,
    status: AssetStatus,
    fields?: Partial<Asset>,
  ): Promise<void>;
  softDelete(id: string, at: Date): Promise<void>;
  list(opts: ListAssetsOpts): Promise<{ items: Asset[]; nextCursor?: string }>;
  recent(opts: { limit: number }): Promise<Asset[]>;
}

export interface ListAssetsOpts {
  projectId: string;
  limit: number;
  cursor?: string;
  includeDeleted?: boolean;
}
