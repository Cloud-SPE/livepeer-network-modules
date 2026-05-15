import { and, desc, eq, sql } from "drizzle-orm";
import { waitlist, } from "../db/schema.js";
export async function createWaitlistEntry(db, input) {
    const [row] = await db
        .insert(waitlist)
        .values({
        email: input.email,
        displayName: input.displayName,
        reason: input.reason,
    })
        .onConflictDoUpdate({
        target: waitlist.email,
        // Idempotent intake: re-submitting the same email refreshes the
        // hint fields but does not advance state. An already-approved
        // user re-submitting won't be re-queued.
        set: {
            displayName: sql `coalesce(excluded.display_name, ${waitlist.displayName})`,
            reason: sql `coalesce(excluded.reason, ${waitlist.reason})`,
        },
    })
        .returning();
    return row;
}
export async function getWaitlistByEmail(db, email) {
    const rows = await db
        .select()
        .from(waitlist)
        .where(eq(waitlist.email, email))
        .limit(1);
    return rows[0] ?? null;
}
export async function getWaitlistById(db, id) {
    const rows = await db
        .select()
        .from(waitlist)
        .where(eq(waitlist.id, id))
        .limit(1);
    return rows[0] ?? null;
}
export async function listWaitlist(db, opts = {}) {
    const limit = Math.min(opts.limit ?? 50, 200);
    const offset = opts.offset ?? 0;
    const where = opts.status ? eq(waitlist.status, opts.status) : undefined;
    const rows = where
        ? await db
            .select()
            .from(waitlist)
            .where(where)
            .orderBy(desc(waitlist.createdAt))
            .limit(limit)
            .offset(offset)
        : await db
            .select()
            .from(waitlist)
            .orderBy(desc(waitlist.createdAt))
            .limit(limit)
            .offset(offset);
    return rows;
}
export async function setWaitlistDecision(db, input) {
    const [row] = await db
        .update(waitlist)
        .set({
        status: input.status,
        decidedBy: input.decidedBy,
        decidedAt: new Date(),
        customerId: input.customerId ?? null,
    })
        .where(and(eq(waitlist.id, input.id), eq(waitlist.status, "pending")))
        .returning();
    return row ?? null;
}
//# sourceMappingURL=waitlist.js.map