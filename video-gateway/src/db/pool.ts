import { drizzle } from "drizzle-orm/node-postgres";
import pg from "pg";

import { schema } from "./schema.js";

export type Db = ReturnType<typeof drizzle<typeof schema>>;

export function createDb(databaseUrl: string): Db {
  const pool = new pg.Pool({ connectionString: databaseUrl, max: 10 });
  return drizzle(pool, { schema });
}

export { schema };
