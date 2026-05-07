import { eq, and, desc, isNull } from 'drizzle-orm';
import type { Db } from '../db/pool.js';
import { apiKeys, customers } from '../db/schema.js';
import type { CustomerRow } from './customers.js';

export type ApiKeyRow = typeof apiKeys.$inferSelect;
export type ApiKeyInsert = typeof apiKeys.$inferInsert;

export async function insertApiKey(db: Db, values: ApiKeyInsert): Promise<ApiKeyRow> {
  const [row] = await db.insert(apiKeys).values(values).returning();
  if (!row) throw new Error('insertApiKey: no row returned');
  return row;
}

export async function findActiveByHash(
  db: Db,
  hash: string,
): Promise<{ apiKey: ApiKeyRow; customer: CustomerRow } | null> {
  const rows = await db
    .select()
    .from(apiKeys)
    .innerJoin(customers, eq(apiKeys.customerId, customers.id))
    .where(and(eq(apiKeys.hash, hash), isNull(apiKeys.revokedAt)))
    .limit(1);
  const row = rows[0];
  if (!row) return null;
  return { apiKey: row.api_keys, customer: row.customers };
}

export async function findById(db: Db, id: string): Promise<ApiKeyRow | null> {
  const rows = await db.select().from(apiKeys).where(eq(apiKeys.id, id)).limit(1);
  return rows[0] ?? null;
}

export async function revoke(db: Db, id: string, revokedAt: Date): Promise<void> {
  await db.update(apiKeys).set({ revokedAt }).where(eq(apiKeys.id, id));
}

export async function markUsed(db: Db, id: string, at: Date): Promise<void> {
  await db.update(apiKeys).set({ lastUsedAt: at }).where(eq(apiKeys.id, id));
}

export async function findByCustomer(db: Db, customerId: string): Promise<ApiKeyRow[]> {
  return db
    .select()
    .from(apiKeys)
    .where(eq(apiKeys.customerId, customerId))
    .orderBy(desc(apiKeys.createdAt));
}

export async function countActiveByCustomer(db: Db, customerId: string): Promise<number> {
  const rows = await db
    .select()
    .from(apiKeys)
    .where(and(eq(apiKeys.customerId, customerId), isNull(apiKeys.revokedAt)));
  return rows.length;
}
