import { and, asc, desc, eq, inArray } from "drizzle-orm";

import type { Db } from "../db/pool.js";
import { encodingJobs } from "../db/schema.js";
import type { EncodingJobRepo } from "../engine/repo/jobRepo.js";
import type { EncodingJob, JobKind, JobStatus } from "../engine/types/index.js";

interface EncodingJobRow {
  id: string;
  assetId: string;
  renditionId: string | null;
  kind: string;
  status: string;
  workerUrl: string | null;
  attemptCount: number;
  inputUrl: string | null;
  outputPrefix: string | null;
  errorMessage: string | null;
  startedAt: Date | null;
  completedAt: Date | null;
  createdAt: Date;
}

function rowToEncodingJob(row: EncodingJobRow): EncodingJob {
  const job: EncodingJob = {
    id: row.id,
    assetId: row.assetId,
    kind: row.kind as JobKind,
    status: row.status as JobStatus,
    attemptCount: row.attemptCount,
    createdAt: row.createdAt,
  };
  if (row.renditionId !== null) job.renditionId = row.renditionId;
  if (row.workerUrl !== null) job.workerUrl = row.workerUrl;
  if (row.inputUrl !== null) job.inputUrl = row.inputUrl;
  if (row.outputPrefix !== null) job.outputPrefix = row.outputPrefix;
  if (row.errorMessage !== null) job.errorMessage = row.errorMessage;
  if (row.startedAt !== null) job.startedAt = row.startedAt;
  if (row.completedAt !== null) job.completedAt = row.completedAt;
  return job;
}

export interface MutableEncodingJobRepo extends EncodingJobRepo {
  deleteByAsset(assetId: string): Promise<void>;
}

export function createEncodingJobRepo(db: Db): MutableEncodingJobRepo {
  return {
    async insert(job) {
      const [row] = await db
        .insert(encodingJobs)
        .values({
          id: job.id,
          assetId: job.assetId,
          renditionId: job.renditionId ?? null,
          kind: job.kind,
          status: job.status,
          workerUrl: job.workerUrl ?? null,
          attemptCount: job.attemptCount ?? 0,
          inputUrl: job.inputUrl ?? null,
          outputPrefix: job.outputPrefix ?? null,
          errorMessage: job.errorMessage ?? null,
          startedAt: job.startedAt ?? null,
          completedAt: job.completedAt ?? null,
          createdAt: job.createdAt ?? new Date(),
        })
        .returning();
      if (!row) throw new Error("createEncodingJobRepo.insert: no row returned");
      return rowToEncodingJob(row as EncodingJobRow);
    },

    async byId(id) {
      const rows = await db.select().from(encodingJobs).where(eq(encodingJobs.id, id)).limit(1);
      const row = rows[0];
      return row ? rowToEncodingJob(row as EncodingJobRow) : null;
    },

    async byAsset(assetId) {
      const rows = await db
        .select()
        .from(encodingJobs)
        .where(eq(encodingJobs.assetId, assetId))
        .orderBy(desc(encodingJobs.createdAt), asc(encodingJobs.id));
      return rows.map((row) => rowToEncodingJob(row as EncodingJobRow));
    },

    async queued(assetId, kinds) {
      if (kinds.length === 0) return [];
      const rows = await db
        .select()
        .from(encodingJobs)
        .where(and(eq(encodingJobs.assetId, assetId), eq(encodingJobs.status, "queued"), inArray(encodingJobs.kind, kinds)))
        .orderBy(asc(encodingJobs.createdAt), asc(encodingJobs.id));
      return rows.map((row) => rowToEncodingJob(row as EncodingJobRow));
    },

    async updateStatus(id, status, fields) {
      await db
        .update(encodingJobs)
        .set({
          status,
          renditionId: fields?.renditionId ?? undefined,
          workerUrl: fields?.workerUrl ?? undefined,
          inputUrl: fields?.inputUrl ?? undefined,
          outputPrefix: fields?.outputPrefix ?? undefined,
          errorMessage: fields?.errorMessage ?? undefined,
          startedAt: fields?.startedAt ?? undefined,
          completedAt: fields?.completedAt ?? undefined,
        })
        .where(eq(encodingJobs.id, id));
    },

    async incrementAttempt(id) {
      const current = await this.byId(id);
      if (!current) return;
      await db
        .update(encodingJobs)
        .set({ attemptCount: current.attemptCount + 1 })
        .where(eq(encodingJobs.id, id));
    },

    async deleteByAsset(assetId) {
      await db.delete(encodingJobs).where(eq(encodingJobs.assetId, assetId));
    },
  };
}
