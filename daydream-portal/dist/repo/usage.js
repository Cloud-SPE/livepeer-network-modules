import { and, desc, eq, gte, sql } from "drizzle-orm";
import { usageEvents } from "../db/schema.js";
export async function openSessionUsage(db, input) {
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
    return row;
}
export async function closeSessionUsage(db, input) {
    const ended = input.endedAt ?? new Date();
    const [row] = await db
        .update(usageEvents)
        .set({
        endedAt: ended,
        bytesIn: input.bytesIn,
        bytesOut: input.bytesOut,
        durationSeconds: sql `extract(epoch from (${ended} - ${usageEvents.startedAt}))::integer`,
    })
        .where(eq(usageEvents.sessionId, input.sessionId))
        .returning();
    return row ?? null;
}
export async function summaryForCustomer(db, customerId, windowStart) {
    const rows = await db
        .select({
        sessions: sql `count(*)::int`,
        seconds: sql `coalesce(sum(${usageEvents.durationSeconds}), 0)::int`,
    })
        .from(usageEvents)
        .where(and(eq(usageEvents.customerId, customerId), gte(usageEvents.startedAt, windowStart)));
    const r = rows[0] ?? { sessions: 0, seconds: 0 };
    return {
        totalSessions: r.sessions,
        totalSeconds: r.seconds,
        windowStart,
    };
}
export async function recentForCustomer(db, customerId, limit = 50) {
    const rows = await db
        .select()
        .from(usageEvents)
        .where(eq(usageEvents.customerId, customerId))
        .orderBy(desc(usageEvents.startedAt))
        .limit(Math.min(limit, 500));
    return rows;
}
export async function adminSummary(db, windowStart) {
    const rows = await db
        .select({
        sessions: sql `count(*)::int`,
        seconds: sql `coalesce(sum(${usageEvents.durationSeconds}), 0)::int`,
        customers: sql `count(distinct ${usageEvents.customerId})::int`,
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
//# sourceMappingURL=usage.js.map