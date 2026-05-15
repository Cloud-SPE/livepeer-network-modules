import type { NodePgDatabase } from "drizzle-orm/node-postgres";
export type UsageDb = NodePgDatabase<Record<string, unknown>>;
export interface UsageEventRow {
    id: string;
    customerId: string;
    apiKeyId: string | null;
    sessionId: string;
    orchestrator: string | null;
    startedAt: Date;
    endedAt: Date | null;
    durationSeconds: number | null;
    bytesIn: bigint | null;
    bytesOut: bigint | null;
    createdAt: Date;
}
export declare function openSessionUsage(db: UsageDb, input: {
    customerId: string;
    apiKeyId?: string;
    sessionId: string;
    orchestrator?: string;
    startedAt?: Date;
}): Promise<UsageEventRow>;
export declare function closeSessionUsage(db: UsageDb, input: {
    sessionId: string;
    endedAt?: Date;
    bytesIn?: bigint;
    bytesOut?: bigint;
}): Promise<UsageEventRow | null>;
export interface UsageSummary {
    totalSessions: number;
    totalSeconds: number;
    windowStart: Date;
}
export declare function summaryForCustomer(db: UsageDb, customerId: string, windowStart: Date): Promise<UsageSummary>;
export declare function recentForCustomer(db: UsageDb, customerId: string, limit?: number): Promise<UsageEventRow[]>;
export interface AdminUsageSummary {
    totalSessions: number;
    totalSeconds: number;
    uniqueCustomers: number;
}
export declare function adminSummary(db: UsageDb, windowStart: Date): Promise<AdminUsageSummary>;
