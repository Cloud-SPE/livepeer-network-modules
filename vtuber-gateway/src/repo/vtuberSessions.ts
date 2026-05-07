import { and, eq } from "drizzle-orm";
import type { NodePgDatabase } from "drizzle-orm/node-postgres";

import { vtuberSession } from "./schema.js";

// drizzle-backed CRUD for `vtuber.session`. Source surface:
// `livepeer-network-suite/livepeer-vtuber-gateway/src/repo/vtuberSessions.ts`.

export interface VtuberSessionRow {
  id: string;
  customerId: string;
  status:
    | "starting"
    | "active"
    | "ending"
    | "ended"
    | "errored";
  paramsJson: string;
  nodeId: string | null;
  nodeUrl: string | null;
  workerSessionId: string | null;
  controlUrl: string;
  expiresAt: Date;
  createdAt: Date;
  endedAt: Date | null;
  errorCode: string | null;
  payerWorkId: string | null;
}

export class VtuberSessionRepo {
  constructor(private readonly db: NodePgDatabase<Record<string, never>>) {}

  async findById(id: string): Promise<VtuberSessionRow | null> {
    const rows = await this.db
      .select()
      .from(vtuberSession)
      .where(eq(vtuberSession.id, id))
      .limit(1);
    return (rows[0] as VtuberSessionRow | undefined) ?? null;
  }

  async findByCustomerAndId(
    customerId: string,
    id: string,
  ): Promise<VtuberSessionRow | null> {
    const rows = await this.db
      .select()
      .from(vtuberSession)
      .where(
        and(eq(vtuberSession.customerId, customerId), eq(vtuberSession.id, id)),
      )
      .limit(1);
    return (rows[0] as VtuberSessionRow | undefined) ?? null;
  }
}
