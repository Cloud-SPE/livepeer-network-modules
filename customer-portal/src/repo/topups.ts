import { eq, and, desc, gte, lt } from 'drizzle-orm';
import type { Db } from '../db/pool.js';
import { topups } from '../db/schema.js';

export type TopupRow = typeof topups.$inferSelect;
export type TopupInsert = typeof topups.$inferInsert;
export type TopupStatus = TopupRow['status'];

export async function insertTopup(db: Db, values: TopupInsert): Promise<TopupRow> {
  const [row] = await db.insert(topups).values(values).returning();
  if (!row) throw new Error('insertTopup: no row returned');
  return row;
}

export async function updateTopupStatus(
  db: Db,
  stripeSessionId: string,
  status: TopupStatus,
): Promise<void> {
  await db.update(topups).set({ status }).where(eq(topups.stripeSessionId, stripeSessionId));
}

export async function findBySession(
  db: Db,
  stripeSessionId: string,
): Promise<TopupRow | null> {
  const rows = await db
    .select()
    .from(topups)
    .where(eq(topups.stripeSessionId, stripeSessionId))
    .limit(1);
  return rows[0] ?? null;
}

export async function findByCustomer(
  db: Db,
  customerId: string,
  options: { limit: number; cursorCreatedAt?: Date },
): Promise<TopupRow[]> {
  const where = options.cursorCreatedAt
    ? and(eq(topups.customerId, customerId), lt(topups.createdAt, options.cursorCreatedAt))
    : eq(topups.customerId, customerId);
  return db.select().from(topups).where(where).orderBy(desc(topups.createdAt)).limit(options.limit);
}

export interface TopupSearchInput {
  customerId?: string;
  status?: TopupStatus;
  from?: Date;
  to?: Date;
  limit: number;
  cursorCreatedAt?: Date;
}

export async function search(db: Db, input: TopupSearchInput): Promise<TopupRow[]> {
  const conds = [];
  if (input.customerId) conds.push(eq(topups.customerId, input.customerId));
  if (input.status) conds.push(eq(topups.status, input.status));
  if (input.from) conds.push(gte(topups.createdAt, input.from));
  if (input.to) conds.push(lt(topups.createdAt, input.to));
  if (input.cursorCreatedAt) conds.push(lt(topups.createdAt, input.cursorCreatedAt));

  const where = conds.length === 0 ? undefined : conds.length === 1 ? conds[0] : and(...conds);
  const builder = db.select().from(topups);
  const filtered = where ? builder.where(where) : builder;
  return filtered.orderBy(desc(topups.createdAt)).limit(input.limit);
}
