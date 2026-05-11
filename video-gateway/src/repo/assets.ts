import { and, asc, desc, eq, isNull, lt } from "drizzle-orm";

import type { Db } from "../db/pool.js";
import { assets } from "../db/schema.js";
import type {
  Asset,
  AssetRepo,
  AssetSourceType,
  AssetStatus,
  EncodingTier,
  ListAssetsOpts,
} from "../engine/index.js";

interface AssetRow {
  id: string;
  projectId: string;
  status: string;
  sourceType: string;
  selectedOffering: string | null;
  sourceUrl: string | null;
  durationSec: string | null;
  width: number | null;
  height: number | null;
  frameRate: string | null;
  audioCodec: string | null;
  videoCodec: string | null;
  encodingTier: string;
  ffprobeJson: unknown;
  errorMessage: string | null;
  createdAt: Date;
  readyAt: Date | null;
  deletedAt: Date | null;
}

function rowToAsset(row: AssetRow): Asset {
  const a: Asset = {
    id: row.id,
    projectId: row.projectId,
    status: row.status as AssetStatus,
    sourceType: row.sourceType as AssetSourceType,
    encodingTier: row.encodingTier as EncodingTier,
    createdAt: row.createdAt,
  };
  if (row.selectedOffering !== null) a.selectedOffering = row.selectedOffering;
  if (row.sourceUrl !== null) a.sourceUrl = row.sourceUrl;
  if (row.durationSec !== null) a.durationSec = Number(row.durationSec);
  if (row.width !== null) a.width = row.width;
  if (row.height !== null) a.height = row.height;
  if (row.frameRate !== null) a.frameRate = Number(row.frameRate);
  if (row.audioCodec !== null) a.audioCodec = row.audioCodec;
  if (row.videoCodec !== null) a.videoCodec = row.videoCodec;
  if (row.ffprobeJson !== null && row.ffprobeJson !== undefined) a.ffprobeJson = row.ffprobeJson;
  if (row.errorMessage !== null) a.errorMessage = row.errorMessage;
  if (row.readyAt !== null) a.readyAt = row.readyAt;
  if (row.deletedAt !== null) a.deletedAt = row.deletedAt;
  return a;
}

function assetToInsert(asset: Omit<Asset, "createdAt"> & { createdAt?: Date }): typeof assets.$inferInsert {
  return {
    id: asset.id,
    projectId: asset.projectId,
    status: asset.status,
    sourceType: asset.sourceType,
    selectedOffering: asset.selectedOffering ?? null,
    sourceUrl: asset.sourceUrl ?? null,
    durationSec: asset.durationSec !== undefined ? String(asset.durationSec) : null,
    width: asset.width ?? null,
    height: asset.height ?? null,
    frameRate: asset.frameRate !== undefined ? String(asset.frameRate) : null,
    audioCodec: asset.audioCodec ?? null,
    videoCodec: asset.videoCodec ?? null,
    encodingTier: asset.encodingTier,
    ffprobeJson: asset.ffprobeJson ?? null,
    errorMessage: asset.errorMessage ?? null,
    createdAt: asset.createdAt ?? new Date(),
    readyAt: asset.readyAt ?? null,
    deletedAt: asset.deletedAt ?? null,
  };
}

export function createAssetRepo(db: Db): AssetRepo {
  return {
    async insert(asset) {
      const [row] = await db.insert(assets).values(assetToInsert(asset)).returning();
      if (!row) throw new Error("createAssetRepo.insert: no row returned");
      return rowToAsset(row as AssetRow);
    },

    async byId(id) {
      const rows = await db.select().from(assets).where(eq(assets.id, id)).limit(1);
      const row = rows[0];
      return row ? rowToAsset(row as AssetRow) : null;
    },

    async byPlaybackId(_playbackId) {
      return null;
    },

    async updateStatus(id, status, fields) {
      const update: Partial<typeof assets.$inferInsert> = { status };
      if (fields?.readyAt) update.readyAt = fields.readyAt;
      if (fields?.errorMessage !== undefined) update.errorMessage = fields.errorMessage;
      if (fields?.durationSec !== undefined) update.durationSec = String(fields.durationSec);
      if (fields?.width !== undefined) update.width = fields.width;
      if (fields?.height !== undefined) update.height = fields.height;
      if (fields?.frameRate !== undefined) update.frameRate = String(fields.frameRate);
      if (fields?.audioCodec !== undefined) update.audioCodec = fields.audioCodec;
      if (fields?.videoCodec !== undefined) update.videoCodec = fields.videoCodec;
      if (fields?.ffprobeJson !== undefined) update.ffprobeJson = fields.ffprobeJson;
      if (fields?.sourceUrl !== undefined) update.sourceUrl = fields.sourceUrl;
      if (fields?.selectedOffering !== undefined) update.selectedOffering = fields.selectedOffering;
      if (fields?.encodingTier !== undefined) update.encodingTier = fields.encodingTier;
      await db.update(assets).set(update).where(eq(assets.id, id));
    },

    async softDelete(id, at) {
      await db
        .update(assets)
        .set({ deletedAt: at, status: "deleted" })
        .where(eq(assets.id, id));
    },

    async list(opts: ListAssetsOpts) {
      const conds = [eq(assets.projectId, opts.projectId)];
      if (!opts.includeDeleted) conds.push(isNull(assets.deletedAt));
      if (opts.cursor) conds.push(lt(assets.createdAt, new Date(opts.cursor)));
      const where = conds.length === 1 ? conds[0] : and(...conds);
      const rows = await db
        .select()
        .from(assets)
        .where(where)
        .orderBy(desc(assets.createdAt), asc(assets.id))
        .limit(opts.limit + 1);
      const items = rows.slice(0, opts.limit).map((r) => rowToAsset(r as AssetRow));
      const result: { items: Asset[]; nextCursor?: string } = { items };
      if (rows.length > opts.limit && items.length > 0) {
        const last = items[items.length - 1]!;
        result.nextCursor = last.createdAt.toISOString();
      }
      return result;
    },

    async recent(opts) {
      const rows = await db
        .select()
        .from(assets)
        .where(isNull(assets.deletedAt))
        .orderBy(desc(assets.createdAt))
        .limit(opts.limit);
      return rows.map((r) => rowToAsset(r as AssetRow));
    },
  };
}
