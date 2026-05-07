export interface Upload {
  id: string;
  projectId: string;
  assetId?: string;
  status: UploadStatus;
  uploadUrl: string;
  storageKey: string;
  expiresAt: Date;
  createdAt: Date;
  completedAt?: Date;
}

export type UploadStatus = "waiting" | "uploading" | "completed" | "expired";
