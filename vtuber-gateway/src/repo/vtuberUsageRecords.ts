import { eq } from "drizzle-orm";
import type { NodePgDatabase } from "drizzle-orm/node-postgres";

import { vtuberUsageRecord } from "./schema.js";

// drizzle-backed CRUD for `vtuber.usage_record`. Source surface:
// `livepeer-network-suite/livepeer-vtuber-gateway/src/repo/{usageRecords,usageRollups}.ts`.

export interface VtuberUsageRecordRow {
  id: string;
  sessionId: string;
  customerId: string;
  seconds: number;
  cents: bigint;
  createdAt: Date;
}

export class VtuberUsageRecordRepo {
  constructor(private readonly db: NodePgDatabase<Record<string, never>>) {}

  async findBySession(sessionId: string): Promise<VtuberUsageRecordRow[]> {
    const rows = await this.db
      .select()
      .from(vtuberUsageRecord)
      .where(eq(vtuberUsageRecord.sessionId, sessionId));
    return rows as VtuberUsageRecordRow[];
  }
}
