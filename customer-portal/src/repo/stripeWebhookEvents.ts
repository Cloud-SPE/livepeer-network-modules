import { sql } from 'drizzle-orm';
import type { Db } from '../db/pool.js';

export async function insertIfNew(
  db: Db,
  eventId: string,
  type: string,
  payload: string,
): Promise<boolean> {
  const result = await db.execute(
    sql`
      INSERT INTO app.stripe_webhook_events (event_id, type, payload)
      VALUES (${eventId}, ${type}, ${payload})
      ON CONFLICT (event_id) DO NOTHING
      RETURNING event_id
    `,
  );
  return result.rows.length > 0;
}
