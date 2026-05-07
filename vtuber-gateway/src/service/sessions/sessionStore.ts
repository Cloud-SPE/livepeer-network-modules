import type { SessionStatus } from "../../types/vtuber.js";

export interface VtuberSessionRecord {
  id: string;
  customerId: string;
  status: SessionStatus;
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

export interface InsertSessionInput {
  id: string;
  customerId: string;
  paramsJson: string;
  nodeId: string;
  nodeUrl: string;
  controlUrl: string;
  expiresAt: Date;
}

export interface InsertBearerInput {
  sessionId: string;
  customerId: string;
  hash: string;
}

export interface UpdateSessionInput {
  status?: SessionStatus;
  workerSessionId?: string | null;
  payerWorkId?: string | null;
  errorCode?: string | null;
  endedAt?: Date | null;
}

export interface SessionStore {
  insertSession(input: InsertSessionInput): Promise<VtuberSessionRecord>;
  insertBearer(input: InsertBearerInput): Promise<void>;
  findById(id: string): Promise<VtuberSessionRecord | null>;
  findByBearerHash(hash: string): Promise<VtuberSessionRecord | null>;
  updateSession(id: string, patch: UpdateSessionInput): Promise<void>;
}
