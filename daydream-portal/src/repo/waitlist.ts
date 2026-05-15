import { and, desc, eq, sql } from "drizzle-orm";
import type { NodePgDatabase } from "drizzle-orm/node-postgres";
import {
  waitlist,
  type WaitlistStatus,
} from "../db/schema.js";

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

export async function createWaitlistEntry(
  db: WaitlistDb,
  input: { email: string; displayName?: string; reason?: string },
): Promise<WaitlistRow> {
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
        displayName: sql`coalesce(excluded.display_name, ${waitlist.displayName})`,
        reason: sql`coalesce(excluded.reason, ${waitlist.reason})`,
      },
    })
    .returning();
  return row as WaitlistRow;
}

export async function getWaitlistByEmail(
  db: WaitlistDb,
  email: string,
): Promise<WaitlistRow | null> {
  const rows = await db
    .select()
    .from(waitlist)
    .where(eq(waitlist.email, email))
    .limit(1);
  return (rows[0] as WaitlistRow | undefined) ?? null;
}

export async function getWaitlistById(
  db: WaitlistDb,
  id: string,
): Promise<WaitlistRow | null> {
  const rows = await db
    .select()
    .from(waitlist)
    .where(eq(waitlist.id, id))
    .limit(1);
  return (rows[0] as WaitlistRow | undefined) ?? null;
}

export async function listWaitlist(
  db: WaitlistDb,
  opts: { status?: WaitlistStatus; limit?: number; offset?: number } = {},
): Promise<WaitlistRow[]> {
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
  return rows as WaitlistRow[];
}

export async function setWaitlistDecision(
  db: WaitlistDb,
  input: {
    id: string;
    status: Exclude<WaitlistStatus, "pending">;
    decidedBy: string;
    customerId?: string;
  },
): Promise<WaitlistRow | null> {
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
  return (row as WaitlistRow | undefined) ?? null;
}
