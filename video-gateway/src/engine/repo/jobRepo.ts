import type {
  EncodingJob,
  JobKind,
  JobStatus,
  Rendition,
  RenditionStatus,
} from "../types/index.js";

export interface EncodingJobRepo {
  insert(
    job: Omit<EncodingJob, "createdAt" | "attemptCount"> & {
      createdAt?: Date;
      attemptCount?: number;
    },
  ): Promise<EncodingJob>;
  byId(id: string): Promise<EncodingJob | null>;
  byAsset(assetId: string): Promise<EncodingJob[]>;
  queued(assetId: string, kinds: JobKind[]): Promise<EncodingJob[]>;
  updateStatus(
    id: string,
    status: JobStatus,
    fields?: Partial<EncodingJob>,
  ): Promise<void>;
  incrementAttempt(id: string): Promise<void>;
}

export interface RenditionRepo {
  insert(r: Omit<Rendition, "createdAt"> & { createdAt?: Date }): Promise<Rendition>;
  byAsset(assetId: string): Promise<Rendition[]>;
  updateStatus(
    id: string,
    status: RenditionStatus,
    fields?: Partial<Rendition>,
  ): Promise<void>;
}
