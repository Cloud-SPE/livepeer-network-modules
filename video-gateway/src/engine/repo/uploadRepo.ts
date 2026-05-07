import type { Upload, UploadStatus } from "../types/index.js";

export interface UploadRepo {
  insert(upload: Omit<Upload, "createdAt"> & { createdAt?: Date }): Promise<Upload>;
  byId(id: string): Promise<Upload | null>;
  updateStatus(
    id: string,
    status: UploadStatus,
    fields?: Partial<Upload>,
  ): Promise<void>;
  sweepExpired(now: Date): Promise<Upload[]>;
}
