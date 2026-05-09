import { drizzle } from "drizzle-orm/node-postgres";
import type { NodePgDatabase } from "drizzle-orm/node-postgres";
import pg from "pg";

export type Db = NodePgDatabase<Record<string, never>>;

export interface PoolConfig {
  connectionString: string;
  max?: number;
}

export function createPool(config: PoolConfig): pg.Pool {
  return new pg.Pool({
    connectionString: config.connectionString,
    max: config.max ?? 10,
  });
}

export function makeDb(pool: pg.Pool): Db {
  return drizzle(pool);
}
