import { desc, eq, inArray } from "drizzle-orm";

import type { Db } from "../db/pool.js";
import { liveSessionDebits, projects, usageRecords } from "../db/schema.js";

export interface UsageRecord {
  id: string;
  projectId: string;
  assetId: string | null;
  liveStreamId: string | null;
  capability: string;
  amountCents: number;
  createdAt: Date;
}

export interface LiveSessionDebit {
  id: string;
  liveStreamId: string;
  amountUsdMicros: bigint;
  durationSec: number;
  createdAt: Date;
}

export interface UsageRecordRepo {
  insert(input: Omit<UsageRecord, "createdAt"> & { createdAt?: Date }): Promise<UsageRecord>;
  listByProjects(projectIds: string[], limit: number): Promise<UsageRecord[]>;
  listByCustomer(customerId: string, limit: number): Promise<UsageRecord[]>;
  recent(limit: number): Promise<UsageRecord[]>;
  byAsset(assetId: string): Promise<UsageRecord[]>;
  byLiveStream(liveStreamId: string): Promise<UsageRecord[]>;
}

export interface LiveSessionDebitRepo {
  insert(input: Omit<LiveSessionDebit, "createdAt"> & { createdAt?: Date }): Promise<LiveSessionDebit>;
  byLiveStream(liveStreamId: string): Promise<LiveSessionDebit[]>;
}

function rowToUsageRecord(row: typeof usageRecords.$inferSelect): UsageRecord {
  return {
    id: row.id,
    projectId: row.projectId,
    assetId: row.assetId,
    liveStreamId: row.liveStreamId,
    capability: row.capability,
    amountCents: Number(row.amountCents),
    createdAt: row.createdAt,
  };
}

function rowToLiveSessionDebit(row: typeof liveSessionDebits.$inferSelect): LiveSessionDebit {
  return {
    id: row.id,
    liveStreamId: row.liveStreamId,
    amountUsdMicros: row.amountUsdMicros,
    durationSec: row.durationSec,
    createdAt: row.createdAt,
  };
}

export function createUsageRecordRepo(db: Db): UsageRecordRepo {
  return {
    async insert(input) {
      const [row] = await db
        .insert(usageRecords)
        .values({
          id: input.id,
          projectId: input.projectId,
          assetId: input.assetId ?? null,
          liveStreamId: input.liveStreamId ?? null,
          capability: input.capability,
          amountCents: String(input.amountCents),
          createdAt: input.createdAt ?? new Date(),
        })
        .returning();
      if (!row) throw new Error("createUsageRecordRepo.insert: no row returned");
      return rowToUsageRecord(row);
    },

    async listByProjects(projectIds, limit) {
      if (projectIds.length === 0) return [];
      const rows = await db
        .select()
        .from(usageRecords)
        .where(inArray(usageRecords.projectId, projectIds))
        .orderBy(desc(usageRecords.createdAt))
        .limit(limit);
      return rows.map(rowToUsageRecord);
    },

    async listByCustomer(customerId, limit) {
      const rows = await db
        .select({
          id: usageRecords.id,
          projectId: usageRecords.projectId,
          assetId: usageRecords.assetId,
          liveStreamId: usageRecords.liveStreamId,
          capability: usageRecords.capability,
          amountCents: usageRecords.amountCents,
          createdAt: usageRecords.createdAt,
        })
        .from(usageRecords)
        .innerJoin(projects, eq(projects.id, usageRecords.projectId))
        .where(eq(projects.customerId, customerId))
        .orderBy(desc(usageRecords.createdAt))
        .limit(limit);
      return rows.map((row) =>
        rowToUsageRecord({
          id: row.id,
          projectId: row.projectId,
          assetId: row.assetId,
          liveStreamId: row.liveStreamId,
          capability: row.capability,
          amountCents: row.amountCents,
          createdAt: row.createdAt,
        }),
      );
    },

    async recent(limit) {
      const rows = await db
        .select()
        .from(usageRecords)
        .orderBy(desc(usageRecords.createdAt))
        .limit(limit);
      return rows.map(rowToUsageRecord);
    },

    async byAsset(assetId) {
      const rows = await db
        .select()
        .from(usageRecords)
        .where(eq(usageRecords.assetId, assetId))
        .orderBy(desc(usageRecords.createdAt));
      return rows.map(rowToUsageRecord);
    },

    async byLiveStream(liveStreamId) {
      const rows = await db
        .select()
        .from(usageRecords)
        .where(eq(usageRecords.liveStreamId, liveStreamId))
        .orderBy(desc(usageRecords.createdAt));
      return rows.map(rowToUsageRecord);
    },
  };
}

export function createLiveSessionDebitRepo(db: Db): LiveSessionDebitRepo {
  return {
    async insert(input) {
      const [row] = await db
        .insert(liveSessionDebits)
        .values({
          id: input.id,
          liveStreamId: input.liveStreamId,
          amountUsdMicros: input.amountUsdMicros,
          durationSec: input.durationSec,
          createdAt: input.createdAt ?? new Date(),
        })
        .returning();
      if (!row) throw new Error("createLiveSessionDebitRepo.insert: no row returned");
      return rowToLiveSessionDebit(row);
    },

    async byLiveStream(liveStreamId) {
      const rows = await db
        .select()
        .from(liveSessionDebits)
        .where(eq(liveSessionDebits.liveStreamId, liveStreamId))
        .orderBy(desc(liveSessionDebits.createdAt));
      return rows.map(rowToLiveSessionDebit);
    },
  };
}
