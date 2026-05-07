import { and, desc, eq, ilike, lt, or, sql } from 'drizzle-orm';
import type { Db } from '../db/pool.js';
import { customers } from '../db/schema.js';

export type CustomerRow = typeof customers.$inferSelect;
export type CustomerInsert = typeof customers.$inferInsert;

export async function insertCustomer(db: Db, values: CustomerInsert): Promise<CustomerRow> {
  const [row] = await db.insert(customers).values(values).returning();
  if (!row) throw new Error('insertCustomer: no row returned');
  return row;
}

export async function findById(db: Db, id: string): Promise<CustomerRow | null> {
  const rows = await db.select().from(customers).where(eq(customers.id, id)).limit(1);
  return rows[0] ?? null;
}

export async function selectForUpdate(db: Db, id: string): Promise<CustomerRow | null> {
  const rows = await db.select().from(customers).where(eq(customers.id, id)).for('update').limit(1);
  return rows[0] ?? null;
}

export async function updateBalanceFields(
  db: Db,
  id: string,
  values: { balanceUsdCents?: bigint; reservedUsdCents?: bigint },
): Promise<void> {
  await db.update(customers).set(values).where(eq(customers.id, id));
}

export async function updateQuotaFields(
  db: Db,
  id: string,
  values: { quotaTokensRemaining?: bigint; quotaReservedTokens?: bigint },
): Promise<void> {
  await db.update(customers).set(values).where(eq(customers.id, id));
}

export async function incrementBalance(db: Db, id: string, deltaCents: bigint): Promise<void> {
  await db
    .update(customers)
    .set({ balanceUsdCents: sql`${customers.balanceUsdCents} + ${deltaCents.toString()}::bigint` })
    .where(eq(customers.id, id));
}

export async function setStatus(
  db: Db,
  id: string,
  status: 'active' | 'suspended' | 'closed',
): Promise<boolean> {
  const result = await db
    .update(customers)
    .set({ status })
    .where(eq(customers.id, id))
    .returning({ id: customers.id });
  return result.length > 0;
}

export interface CustomerSearchInput {
  q?: string;
  tier?: 'free' | 'prepaid';
  status?: 'active' | 'suspended' | 'closed';
  limit: number;
  cursorCreatedAt?: Date;
}

export async function search(db: Db, input: CustomerSearchInput): Promise<CustomerRow[]> {
  const conds = [];
  if (input.q && input.q.length > 0) {
    const like = `%${input.q}%`;
    const isUuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(input.q);
    conds.push(
      isUuid
        ? or(ilike(customers.email, like), eq(customers.id, input.q))
        : ilike(customers.email, like),
    );
  }
  if (input.tier) conds.push(eq(customers.tier, input.tier));
  if (input.status) conds.push(eq(customers.status, input.status));
  if (input.cursorCreatedAt) conds.push(lt(customers.createdAt, input.cursorCreatedAt));

  const where = conds.length === 0 ? undefined : conds.length === 1 ? conds[0] : and(...conds);

  const builder = db.select().from(customers);
  const filtered = where ? builder.where(where) : builder;
  return filtered.orderBy(desc(customers.createdAt), desc(customers.id)).limit(input.limit);
}
