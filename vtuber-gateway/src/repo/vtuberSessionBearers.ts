import { and, eq, isNull } from "drizzle-orm";
import type { NodePgDatabase } from "drizzle-orm/node-postgres";

import { vtuberSessionBearer } from "./schema.js";

// drizzle-backed CRUD for `vtuber.session_bearer`. Source surface:
// `livepeer-network-suite/livepeer-vtuber-gateway/src/repo/vtuberSessionBearers.ts`.

export interface VtuberSessionBearerRow {
  id: string;
  customerId: string;
  sessionId: string;
  hash: string;
  createdAt: Date;
  lastUsedAt: Date | null;
  revokedAt: Date | null;
}

export class VtuberSessionBearerRepo {
  constructor(private readonly db: NodePgDatabase<Record<string, never>>) {}

  async findActiveByHash(hash: string): Promise<VtuberSessionBearerRow | null> {
    const rows = await this.db
      .select()
      .from(vtuberSessionBearer)
      .where(
        and(
          eq(vtuberSessionBearer.hash, hash),
          isNull(vtuberSessionBearer.revokedAt),
        ),
      )
      .limit(1);
    return (rows[0] as VtuberSessionBearerRow | undefined) ?? null;
  }
}
