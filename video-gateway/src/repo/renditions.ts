import { asc, eq } from "drizzle-orm";

import type { Db } from "../db/pool.js";
import { renditions } from "../db/schema.js";
import type { RenditionRepo } from "../engine/repo/jobRepo.js";
import type { Codec, Rendition, RenditionStatus, Resolution } from "../engine/types/index.js";

interface RenditionRow {
  id: string;
  assetId: string;
  resolution: string;
  codec: string;
  bitrateKbps: number;
  storageKey: string | null;
  status: string;
  durationSec: string | null;
  createdAt: Date;
  completedAt: Date | null;
}

function rowToRendition(row: RenditionRow): Rendition {
  const rendition: Rendition = {
    id: row.id,
    assetId: row.assetId,
    resolution: row.resolution as Resolution,
    codec: row.codec as Codec,
    bitrateKbps: row.bitrateKbps,
    status: row.status as RenditionStatus,
    createdAt: row.createdAt,
  };
  if (row.storageKey !== null) rendition.storageKey = row.storageKey;
  if (row.durationSec !== null) rendition.durationSec = Number(row.durationSec);
  if (row.completedAt !== null) rendition.completedAt = row.completedAt;
  return rendition;
}

export interface MutableRenditionRepo extends RenditionRepo {
  deleteByAsset(assetId: string): Promise<void>;
}

export function createRenditionRepo(db: Db): MutableRenditionRepo {
  return {
    async insert(r) {
      const [row] = await db
        .insert(renditions)
        .values({
          id: r.id,
          assetId: r.assetId,
          resolution: r.resolution,
          codec: r.codec,
          bitrateKbps: r.bitrateKbps,
          storageKey: r.storageKey ?? null,
          status: r.status,
          durationSec: r.durationSec !== undefined ? String(r.durationSec) : null,
          createdAt: r.createdAt ?? new Date(),
          completedAt: r.completedAt ?? null,
        })
        .returning();
      if (!row) throw new Error("createRenditionRepo.insert: no row returned");
      return rowToRendition(row as RenditionRow);
    },

    async byAsset(assetId) {
      const rows = await db
        .select()
        .from(renditions)
        .where(eq(renditions.assetId, assetId))
        .orderBy(asc(renditions.createdAt), asc(renditions.id));
      return rows.map((row) => rowToRendition(row as RenditionRow));
    },

    async updateStatus(id, status, fields) {
      await db
        .update(renditions)
        .set({
          status,
          resolution: fields?.resolution ?? undefined,
          codec: fields?.codec ?? undefined,
          bitrateKbps: fields?.bitrateKbps ?? undefined,
          storageKey: fields?.storageKey ?? undefined,
          durationSec: fields?.durationSec !== undefined ? String(fields.durationSec) : undefined,
          completedAt: fields?.completedAt ?? undefined,
        })
        .where(eq(renditions.id, id));
    },

    async deleteByAsset(assetId) {
      await db.delete(renditions).where(eq(renditions.assetId, assetId));
    },
  };
}
