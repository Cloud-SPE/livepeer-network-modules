import { desc, eq } from "drizzle-orm";

import type { Db } from "../db/pool.js";
import { recordings } from "../db/schema.js";

export type RecordingStatus = "pending" | "running" | "ready" | "failed";

export interface Recording {
  id: string;
  liveStreamId: string;
  assetId: string | null;
  status: RecordingStatus;
  startedAt: Date | null;
  endedAt: Date | null;
  createdAt: Date;
}

export interface RecordingRepo {
  insert(rec: Omit<Recording, "createdAt"> & { createdAt?: Date }): Promise<Recording>;
  byId(id: string): Promise<Recording | null>;
  byLiveStream(liveStreamId: string): Promise<Recording[]>;
  updateStatus(
    id: string,
    status: RecordingStatus,
    fields?: Partial<Pick<Recording, "assetId" | "startedAt" | "endedAt">>,
  ): Promise<void>;
}

interface RecordingRow {
  id: string;
  liveStreamId: string;
  assetId: string | null;
  status: string;
  startedAt: Date | null;
  endedAt: Date | null;
  createdAt: Date;
}

function rowToRecording(row: RecordingRow): Recording {
  return {
    id: row.id,
    liveStreamId: row.liveStreamId,
    assetId: row.assetId,
    status: row.status as RecordingStatus,
    startedAt: row.startedAt,
    endedAt: row.endedAt,
    createdAt: row.createdAt,
  };
}

export function createRecordingRepo(db: Db): RecordingRepo {
  return {
    async insert(rec) {
      const [row] = await db
        .insert(recordings)
        .values({
          id: rec.id,
          liveStreamId: rec.liveStreamId,
          assetId: rec.assetId,
          status: rec.status,
          startedAt: rec.startedAt,
          endedAt: rec.endedAt,
          createdAt: rec.createdAt ?? new Date(),
        })
        .returning();
      if (!row) throw new Error("createRecordingRepo.insert: no row returned");
      return rowToRecording(row as RecordingRow);
    },

    async byId(id) {
      const rows = await db.select().from(recordings).where(eq(recordings.id, id)).limit(1);
      const row = rows[0];
      return row ? rowToRecording(row as RecordingRow) : null;
    },

    async byLiveStream(liveStreamId) {
      const rows = await db
        .select()
        .from(recordings)
        .where(eq(recordings.liveStreamId, liveStreamId))
        .orderBy(desc(recordings.createdAt));
      return rows.map((r) => rowToRecording(r as RecordingRow));
    },

    async updateStatus(id, status, fields) {
      const update: Partial<typeof recordings.$inferInsert> = { status };
      if (fields?.assetId !== undefined) update.assetId = fields.assetId;
      if (fields?.startedAt !== undefined) update.startedAt = fields.startedAt;
      if (fields?.endedAt !== undefined) update.endedAt = fields.endedAt;
      await db.update(recordings).set(update).where(eq(recordings.id, id));
    },
  };
}
