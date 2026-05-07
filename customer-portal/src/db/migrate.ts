import { readdir, readFile } from 'node:fs/promises';
import path from 'node:path';
import { sql } from 'drizzle-orm';
import type { Db } from './pool.js';

const ADVISORY_LOCK_KEY = BigInt('0x6c69767065657273686c'); // 'livpeershl'

export async function runMigrations(db: Db, migrationsDir: string): Promise<void> {
  await db.execute(sql`SELECT pg_advisory_lock(${ADVISORY_LOCK_KEY.toString()})`);
  try {
    await db.execute(sql`
      CREATE TABLE IF NOT EXISTS public._app_migrations (
        filename TEXT PRIMARY KEY,
        applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
      )
    `);

    const entries = await readdir(migrationsDir);
    const sqlFiles = entries.filter((f) => f.endsWith('.sql')).sort();

    const appliedRows = await db.execute<{ filename: string }>(
      sql`SELECT filename FROM public._app_migrations`,
    );
    const applied = new Set(appliedRows.rows.map((r) => r.filename));

    for (const file of sqlFiles) {
      if (applied.has(file)) continue;
      const filepath = path.join(migrationsDir, file);
      const body = await readFile(filepath, 'utf8');
      await db.execute(sql.raw(body));
      await db.execute(
        sql`INSERT INTO public._app_migrations (filename) VALUES (${file})`,
      );
    }
  } finally {
    await db.execute(sql`SELECT pg_advisory_unlock(${ADVISORY_LOCK_KEY.toString()})`);
  }
}
