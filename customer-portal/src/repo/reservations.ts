import { and, asc, desc, eq, gt } from 'drizzle-orm';
import type { Db } from '../db/pool.js';
import { reservations } from '../db/schema.js';

export type ReservationRow = typeof reservations.$inferSelect;
export type ReservationInsert = typeof reservations.$inferInsert;
export type ReservationState = ReservationRow['state'];

export async function insertReservation(
  db: Db,
  values: ReservationInsert,
): Promise<ReservationRow> {
  const [row] = await db.insert(reservations).values(values).returning();
  if (!row) throw new Error('insertReservation: no row returned');
  return row;
}

export async function findById(db: Db, id: string): Promise<ReservationRow | null> {
  const rows = await db.select().from(reservations).where(eq(reservations.id, id)).limit(1);
  return rows[0] ?? null;
}

export async function findByWorkId(db: Db, workId: string): Promise<ReservationRow | null> {
  const rows = await db
    .select()
    .from(reservations)
    .where(eq(reservations.workId, workId))
    .limit(1);
  return rows[0] ?? null;
}

export async function updateState(
  db: Db,
  id: string,
  values: {
    state: ReservationState;
    resolvedAt: Date;
    committedUsdCents?: bigint | null;
    committedTokens?: bigint | null;
    refundedUsdCents?: bigint | null;
    refundedTokens?: bigint | null;
    capability?: string | null;
    model?: string | null;
  },
): Promise<void> {
  await db
    .update(reservations)
    .set({
      state: values.state,
      resolvedAt: values.resolvedAt,
      committedUsdCents: values.committedUsdCents,
      committedTokens: values.committedTokens,
      refundedUsdCents: values.refundedUsdCents,
      refundedTokens: values.refundedTokens,
      capability: values.capability,
      model: values.model,
    })
    .where(eq(reservations.id, id));
}

export async function listByState(
  db: Db,
  options: { state: ReservationState; limit: number; cursorCreatedAt?: Date },
): Promise<ReservationRow[]> {
  const where = options.cursorCreatedAt
    ? and(
        eq(reservations.state, options.state),
        gt(reservations.createdAt, options.cursorCreatedAt),
      )
    : eq(reservations.state, options.state);
  return db
    .select()
    .from(reservations)
    .where(where)
    .orderBy(asc(reservations.createdAt))
    .limit(options.limit);
}

export async function listByCustomer(
  db: Db,
  options: { customerId: string; limit: number },
): Promise<ReservationRow[]> {
  return db
    .select()
    .from(reservations)
    .where(eq(reservations.customerId, options.customerId))
    .orderBy(desc(reservations.createdAt))
    .limit(options.limit);
}
