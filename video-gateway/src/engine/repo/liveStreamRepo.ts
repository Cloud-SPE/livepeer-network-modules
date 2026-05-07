import type { LiveStream, LiveStreamStatus } from "../types/index.js";

export interface LiveStreamRepo {
  insert(stream: Omit<LiveStream, "createdAt"> & { createdAt?: Date }): Promise<LiveStream>;
  byId(id: string): Promise<LiveStream | null>;
  byStreamKeyHash(hash: string): Promise<LiveStream | null>;
  updateStatus(
    id: string,
    status: LiveStreamStatus,
    fields?: Partial<LiveStream>,
  ): Promise<void>;
  active(): Promise<LiveStream[]>;
  sweepStale(cutoff: Date): Promise<LiveStream[]>;
}
