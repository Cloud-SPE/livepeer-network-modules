import type {
  InsertBearerInput,
  InsertSessionInput,
  SessionStore,
  UpdateSessionInput,
  VtuberSessionRecord,
} from "./sessionStore.js";

interface BearerRow {
  sessionId: string;
  customerId: string;
  hash: string;
}

export interface InMemorySessionStore extends SessionStore {
  snapshot(): readonly VtuberSessionRecord[];
}

export function createInMemorySessionStore(): InMemorySessionStore {
  const sessions = new Map<string, VtuberSessionRecord>();
  const bearers = new Map<string, BearerRow>();

  return {
    async insertSession(input: InsertSessionInput): Promise<VtuberSessionRecord> {
      const now = new Date();
      const row: VtuberSessionRecord = {
        id: input.id,
        customerId: input.customerId,
        status: "starting",
        paramsJson: input.paramsJson,
        nodeId: input.nodeId,
        nodeUrl: input.nodeUrl,
        workerSessionId: null,
        controlUrl: input.controlUrl,
        expiresAt: input.expiresAt,
        createdAt: now,
        endedAt: null,
        errorCode: null,
        payerWorkId: null,
      };
      sessions.set(input.id, row);
      return row;
    },
    async insertBearer(input: InsertBearerInput): Promise<void> {
      bearers.set(input.hash, {
        sessionId: input.sessionId,
        customerId: input.customerId,
        hash: input.hash,
      });
    },
    async findById(id: string): Promise<VtuberSessionRecord | null> {
      return sessions.get(id) ?? null;
    },
    async findByBearerHash(hash: string): Promise<VtuberSessionRecord | null> {
      const bearer = bearers.get(hash);
      if (bearer === undefined) {
        return null;
      }
      return sessions.get(bearer.sessionId) ?? null;
    },
    async updateSession(id: string, patch: UpdateSessionInput): Promise<void> {
      const row = sessions.get(id);
      if (row === undefined) {
        return;
      }
      const next: VtuberSessionRecord = {
        ...row,
        ...(patch.status !== undefined ? { status: patch.status } : {}),
        ...(patch.workerSessionId !== undefined
          ? { workerSessionId: patch.workerSessionId }
          : {}),
        ...(patch.payerWorkId !== undefined
          ? { payerWorkId: patch.payerWorkId }
          : {}),
        ...(patch.errorCode !== undefined ? { errorCode: patch.errorCode } : {}),
        ...(patch.endedAt !== undefined ? { endedAt: patch.endedAt } : {}),
      };
      sessions.set(id, next);
    },
    snapshot() {
      return Array.from(sessions.values());
    },
  };
}
