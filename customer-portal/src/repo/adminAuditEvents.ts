import { and, desc, eq, gte, ilike, lt } from 'drizzle-orm';
import type { Db } from '../db/pool.js';
import { adminAuditEvents } from '../db/schema.js';

export type AdminAuditEventRow = typeof adminAuditEvents.$inferSelect;
export type AdminAuditEventInsert = typeof adminAuditEvents.$inferInsert;

export async function recordEvent(
  db: Db,
  values: AdminAuditEventInsert,
): Promise<AdminAuditEventRow> {
  const [row] = await db.insert(adminAuditEvents).values(values).returning();
  if (!row) throw new Error('recordEvent: no row returned');
  return row;
}

export interface AuditSearchInput {
  from?: Date;
  to?: Date;
  actor?: string;
  action?: string;
  limit: number;
  cursorOccurredAt?: Date;
}

export async function search(db: Db, input: AuditSearchInput): Promise<AdminAuditEventRow[]> {
  const conds = [];
  if (input.from) conds.push(gte(adminAuditEvents.occurredAt, input.from));
  if (input.to) conds.push(lt(adminAuditEvents.occurredAt, input.to));
  if (input.actor) conds.push(eq(adminAuditEvents.actor, input.actor));
  if (input.action) conds.push(ilike(adminAuditEvents.action, `%${input.action}%`));
  if (input.cursorOccurredAt) conds.push(lt(adminAuditEvents.occurredAt, input.cursorOccurredAt));

  const where = conds.length === 0 ? undefined : conds.length === 1 ? conds[0] : and(...conds);
  const builder = db.select().from(adminAuditEvents);
  const filtered = where ? builder.where(where) : builder;
  return filtered.orderBy(desc(adminAuditEvents.occurredAt)).limit(input.limit);
}
