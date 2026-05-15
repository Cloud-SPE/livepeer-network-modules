import { and, desc, eq, gte, sql } from "drizzle-orm";
import type { NodePgDatabase } from "drizzle-orm/node-postgres";
import { usageEvents } from "../db/schema.js";

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

export async function openSessionUsage(
  db: UsageDb,
  input: {
    customerId: string;
    apiKeyId?: string;
    sessionId: string;
    orchestrator?: string;
    startedAt?: Date;
  },
): Promise<UsageEventRow> {
  const [row] = await db
    .insert(usageEvents)
    .values({
      customerId: input.customerId,
      apiKeyId: input.apiKeyId,
      sessionId: input.sessionId,
      orchestrator: input.orchestrator,
      startedAt: input.startedAt ?? new Date(),
    })
    .returning();
  return row as UsageEventRow;
}

export async function closeSessionUsage(
  db: UsageDb,
  input: {
    sessionId: string;
    endedAt?: Date;
    bytesIn?: bigint;
    bytesOut?: bigint;
  },
): Promise<UsageEventRow | null> {
  const ended = input.endedAt ?? new Date();
  const [row] = await db
    .update(usageEvents)
    .set({
      endedAt: ended,
      bytesIn: input.bytesIn,
      bytesOut: input.bytesOut,
      durationSeconds: sql`extract(epoch from (${ended} - ${usageEvents.startedAt}))::integer`,
    })
    .where(eq(usageEvents.sessionId, input.sessionId))
    .returning();
  return (row as UsageEventRow | undefined) ?? null;
}

export interface UsageSummary {
  totalSessions: number;
  totalSeconds: number;
  windowStart: Date;
}

export async function summaryForCustomer(
  db: UsageDb,
  customerId: string,
  windowStart: Date,
): Promise<UsageSummary> {
  const rows = await db
    .select({
      sessions: sql<number>`count(*)::int`,
      seconds: sql<number>`coalesce(sum(${usageEvents.durationSeconds}), 0)::int`,
    })
    .from(usageEvents)
    .where(
      and(
        eq(usageEvents.customerId, customerId),
        gte(usageEvents.startedAt, windowStart),
      ),
    );
  const r = rows[0] ?? { sessions: 0, seconds: 0 };
  return {
    totalSessions: r.sessions,
    totalSeconds: r.seconds,
    windowStart,
  };
}

export async function recentForCustomer(
  db: UsageDb,
  customerId: string,
  limit = 50,
): Promise<UsageEventRow[]> {
  const rows = await db
    .select()
    .from(usageEvents)
    .where(eq(usageEvents.customerId, customerId))
    .orderBy(desc(usageEvents.startedAt))
    .limit(Math.min(limit, 500));
  return rows as UsageEventRow[];
}

export interface AdminUsageSummary {
  totalSessions: number;
  totalSeconds: number;
  uniqueCustomers: number;
}

export async function adminSummary(
  db: UsageDb,
  windowStart: Date,
): Promise<AdminUsageSummary> {
  const rows = await db
    .select({
      sessions: sql<number>`count(*)::int`,
      seconds: sql<number>`coalesce(sum(${usageEvents.durationSeconds}), 0)::int`,
      customers: sql<number>`count(distinct ${usageEvents.customerId})::int`,
    })
    .from(usageEvents)
    .where(gte(usageEvents.startedAt, windowStart));
  const r = rows[0] ?? { sessions: 0, seconds: 0, customers: 0 };
  return {
    totalSessions: r.sessions,
    totalSeconds: r.seconds,
    uniqueCustomers: r.customers,
  };
}
