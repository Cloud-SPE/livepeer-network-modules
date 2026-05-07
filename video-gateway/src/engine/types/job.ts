export interface EncodingJob {
  id: string;
  assetId: string;
  renditionId?: string;
  kind: JobKind;
  status: JobStatus;
  workerUrl?: string;
  attemptCount: number;
  inputUrl?: string;
  outputPrefix?: string;
  errorMessage?: string;
  startedAt?: Date;
  completedAt?: Date;
  createdAt: Date;
}

export type JobKind = "probe" | "encode" | "package" | "thumbnail" | "finalize";
export type JobStatus = "queued" | "running" | "completed" | "failed";
