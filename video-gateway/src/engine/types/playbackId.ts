export interface PlaybackId {
  id: string;
  projectId: string;
  assetId?: string;
  liveStreamId?: string;
  policy: PlaybackPolicy;
  tokenRequired: boolean;
  createdAt: Date;
}

export type PlaybackPolicy = "public" | "signed";
