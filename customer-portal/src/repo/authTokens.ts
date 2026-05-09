import { and, desc, eq, isNull } from 'drizzle-orm';
import type { Db } from '../db/pool.js';
import { authTokens, customers } from '../db/schema.js';
import type { CustomerRow } from './customers.js';

export type AuthTokenRow = typeof authTokens.$inferSelect;
export type AuthTokenInsert = typeof authTokens.$inferInsert;

export async function insertAuthToken(db: Db, values: AuthTokenInsert): Promise<AuthTokenRow> {
  const [row] = await db.insert(authTokens).values(values).returning();
  if (!row) throw new Error('insertAuthToken: no row returned');
  return row;
}

export async function findActiveByHash(
  db: Db,
  hash: string,
): Promise<{ authToken: AuthTokenRow; customer: CustomerRow } | null> {
  const rows = await db
    .select()
    .from(authTokens)
    .innerJoin(customers, eq(authTokens.customerId, customers.id))
    .where(and(eq(authTokens.hash, hash), isNull(authTokens.revokedAt)))
    .limit(1);
  const row = rows[0];
  if (!row) return null;
  return { authToken: row.auth_tokens, customer: row.customers };
}

export async function findById(db: Db, id: string): Promise<AuthTokenRow | null> {
  const rows = await db.select().from(authTokens).where(eq(authTokens.id, id)).limit(1);
  return rows[0] ?? null;
}

export async function findByCustomer(db: Db, customerId: string): Promise<AuthTokenRow[]> {
  return db
    .select()
    .from(authTokens)
    .where(eq(authTokens.customerId, customerId))
    .orderBy(desc(authTokens.createdAt));
}

export async function countActiveByCustomer(db: Db, customerId: string): Promise<number> {
  const rows = await db
    .select()
    .from(authTokens)
    .where(and(eq(authTokens.customerId, customerId), isNull(authTokens.revokedAt)));
  return rows.length;
}

export async function markUsed(db: Db, id: string, at: Date): Promise<void> {
  await db.update(authTokens).set({ lastUsedAt: at }).where(eq(authTokens.id, id));
}

export async function revoke(db: Db, id: string, revokedAt: Date): Promise<void> {
  await db.update(authTokens).set({ revokedAt }).where(eq(authTokens.id, id));
}
