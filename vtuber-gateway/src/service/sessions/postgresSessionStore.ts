import { and, desc, eq, isNull } from "drizzle-orm";

import type { Db } from "../../db/pool.js";
import { vtuberSession, vtuberSessionBearer } from "../../repo/schema.js";
import type {
  InsertBearerInput,
  InsertSessionInput,
  ListSessionsInput,
  SessionStore,
  UpdateSessionInput,
  VtuberSessionRecord,
} from "./sessionStore.js";

export function createPostgresSessionStore(db: Db): SessionStore {
  return {
    async insertSession(input: InsertSessionInput): Promise<VtuberSessionRecord> {
      const rows = await db
        .insert(vtuberSession)
        .values({
          id: input.id,
          customerId: input.customerId,
          paramsJson: input.paramsJson,
          nodeId: input.nodeId,
          nodeUrl: input.nodeUrl,
          controlUrl: input.controlUrl,
          expiresAt: input.expiresAt,
        })
        .returning();
      return toRecord(rows[0]!);
    },
    async insertBearer(input: InsertBearerInput): Promise<void> {
      await db.insert(vtuberSessionBearer).values({
        customerId: input.customerId,
        sessionId: input.sessionId,
        hash: input.hash,
      });
    },
    async findById(id: string): Promise<VtuberSessionRecord | null> {
      const rows = await db
        .select()
        .from(vtuberSession)
        .where(eq(vtuberSession.id, id))
        .limit(1);
      return rows[0] ? toRecord(rows[0]) : null;
    },
    async findByBearerHash(hash: string): Promise<VtuberSessionRecord | null> {
      const rows = await db
        .select({ session: vtuberSession })
        .from(vtuberSessionBearer)
        .innerJoin(vtuberSession, eq(vtuberSessionBearer.sessionId, vtuberSession.id))
        .where(and(eq(vtuberSessionBearer.hash, hash), isNull(vtuberSessionBearer.revokedAt)))
        .limit(1);
      return rows[0] ? toRecord(rows[0].session) : null;
    },
    async updateSession(id: string, patch: UpdateSessionInput): Promise<void> {
      if (
        patch.status === undefined &&
        patch.workerSessionId === undefined &&
        patch.controlUrl === undefined &&
        patch.payerWorkId === undefined &&
        patch.errorCode === undefined &&
        patch.endedAt === undefined
      ) {
        return;
      }
      await db
        .update(vtuberSession)
        .set({
          ...(patch.status !== undefined ? { status: patch.status } : {}),
          ...(patch.workerSessionId !== undefined
            ? { workerSessionId: patch.workerSessionId }
            : {}),
          ...(patch.controlUrl !== undefined ? { controlUrl: patch.controlUrl } : {}),
          ...(patch.payerWorkId !== undefined ? { payerWorkId: patch.payerWorkId } : {}),
          ...(patch.errorCode !== undefined ? { errorCode: patch.errorCode } : {}),
          ...(patch.endedAt !== undefined ? { endedAt: patch.endedAt } : {}),
        })
        .where(eq(vtuberSession.id, id));
    },
    async listSessions(input?: ListSessionsInput): Promise<readonly VtuberSessionRecord[]> {
      const limit = input?.limit ?? 100;
      const rows = await db
        .select()
        .from(vtuberSession)
        .where(input?.customerId ? eq(vtuberSession.customerId, input.customerId) : undefined)
        .orderBy(desc(vtuberSession.createdAt))
        .limit(limit);
      return rows.map(toRecord);
    },
  };
}

function toRecord(row: typeof vtuberSession.$inferSelect): VtuberSessionRecord {
  return {
    id: row.id,
    customerId: row.customerId,
    status: row.status,
    paramsJson: row.paramsJson,
    nodeId: row.nodeId,
    nodeUrl: row.nodeUrl,
    workerSessionId: row.workerSessionId,
    controlUrl: row.controlUrl,
    expiresAt: row.expiresAt,
    createdAt: row.createdAt,
    endedAt: row.endedAt,
    errorCode: row.errorCode,
    payerWorkId: row.payerWorkId,
  };
}
