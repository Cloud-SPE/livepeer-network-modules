import type { NodePgDatabase } from "drizzle-orm/node-postgres";
import { type WaitlistStatus } from "../db/schema.js";
export type WaitlistDb = NodePgDatabase<Record<string, unknown>>;
export interface WaitlistRow {
    id: string;
    email: string;
    displayName: string | null;
    reason: string | null;
    status: WaitlistStatus;
    customerId: string | null;
    decidedBy: string | null;
    decidedAt: Date | null;
    createdAt: Date;
}
export declare function createWaitlistEntry(db: WaitlistDb, input: {
    email: string;
    displayName?: string;
    reason?: string;
}): Promise<WaitlistRow>;
export declare function getWaitlistByEmail(db: WaitlistDb, email: string): Promise<WaitlistRow | null>;
export declare function getWaitlistById(db: WaitlistDb, id: string): Promise<WaitlistRow | null>;
export declare function listWaitlist(db: WaitlistDb, opts?: {
    status?: WaitlistStatus;
    limit?: number;
    offset?: number;
}): Promise<WaitlistRow[]>;
export declare function setWaitlistDecision(db: WaitlistDb, input: {
    id: string;
    status: Exclude<WaitlistStatus, "pending">;
    decidedBy: string;
    customerId?: string;
}): Promise<WaitlistRow | null>;
