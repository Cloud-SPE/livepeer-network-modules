import { asc, eq } from "drizzle-orm";

import type { Db } from "../db/pool.js";
import { playbackIds } from "../db/schema.js";

export interface PlaybackIdRecord {
  id: string;
  projectId: string;
  assetId: string | null;
  liveStreamId: string | null;
  policy: string;
  tokenRequired: boolean;
  createdAt: Date;
}

export interface PlaybackIdRepo {
  insert(row: Omit<PlaybackIdRecord, "createdAt"> & { createdAt?: Date }): Promise<PlaybackIdRecord>;
  byId(id: string): Promise<PlaybackIdRecord | null>;
  byAsset(assetId: string): Promise<PlaybackIdRecord[]>;
  recent(limit: number): Promise<PlaybackIdRecord[]>;
  updatePolicy(id: string, fields: { policy?: string; tokenRequired?: boolean }): Promise<void>;
  deleteByAsset(assetId: string): Promise<void>;
}

function rowToPlaybackId(row: typeof playbackIds.$inferSelect): PlaybackIdRecord {
  return {
    id: row.id,
    projectId: row.projectId,
    assetId: row.assetId,
    liveStreamId: row.liveStreamId,
    policy: row.policy,
    tokenRequired: row.tokenRequired,
    createdAt: row.createdAt,
  };
}

export function createPlaybackIdRepo(db: Db): PlaybackIdRepo {
  return {
    async insert(row) {
      const [inserted] = await db
        .insert(playbackIds)
        .values({
          id: row.id,
          projectId: row.projectId,
          assetId: row.assetId ?? null,
          liveStreamId: row.liveStreamId ?? null,
          policy: row.policy,
          tokenRequired: row.tokenRequired,
          createdAt: row.createdAt ?? new Date(),
        })
        .returning();
      if (!inserted) throw new Error("createPlaybackIdRepo.insert: no row returned");
      return rowToPlaybackId(inserted);
    },

    async byId(id) {
      const rows = await db.select().from(playbackIds).where(eq(playbackIds.id, id)).limit(1);
      const row = rows[0];
      return row ? rowToPlaybackId(row) : null;
    },

    async byAsset(assetId) {
      const rows = await db
        .select()
        .from(playbackIds)
        .where(eq(playbackIds.assetId, assetId))
        .orderBy(asc(playbackIds.createdAt), asc(playbackIds.id));
      return rows.map((row) => rowToPlaybackId(row));
    },

    async recent(limit) {
      const rows = await db
        .select()
        .from(playbackIds)
        .orderBy(asc(playbackIds.createdAt), asc(playbackIds.id));
      return rows.slice(-limit).reverse().map((row) => rowToPlaybackId(row));
    },

    async updatePolicy(id, fields) {
      await db
        .update(playbackIds)
        .set({
          policy: fields.policy ?? undefined,
          tokenRequired: fields.tokenRequired ?? undefined,
        })
        .where(eq(playbackIds.id, id));
    },

    async deleteByAsset(assetId) {
      await db.delete(playbackIds).where(eq(playbackIds.assetId, assetId));
    },
  };
}
