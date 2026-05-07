import { eq } from "drizzle-orm";
import type { NodePgDatabase } from "drizzle-orm/node-postgres";

import { vtuberNodeHealth } from "./schema.js";

// drizzle-backed CRUD for `vtuber.node_health`. Source surface:
// `livepeer-network-suite/livepeer-vtuber-gateway/src/repo/nodeHealth.ts`.

export interface VtuberNodeHealthRow {
  nodeId: string;
  nodeUrl: string;
  lastSuccessAt: Date | null;
  lastFailureAt: Date | null;
  consecutiveFails: number;
  circuitOpen: boolean;
  updatedAt: Date;
}

export class VtuberNodeHealthRepo {
  constructor(private readonly db: NodePgDatabase<Record<string, never>>) {}

  async findById(nodeId: string): Promise<VtuberNodeHealthRow | null> {
    const rows = await this.db
      .select()
      .from(vtuberNodeHealth)
      .where(eq(vtuberNodeHealth.nodeId, nodeId))
      .limit(1);
    return (rows[0] as VtuberNodeHealthRow | undefined) ?? null;
  }
}
