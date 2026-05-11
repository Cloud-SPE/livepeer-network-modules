import { asc, eq } from "drizzle-orm";

import type { Db } from "../db/pool.js";
import { projects } from "../db/schema.js";

export interface ProjectRecord {
  id: string;
  customerId: string;
  name: string;
  createdAt: Date;
}

export async function listProjectsForCustomer(db: Db, customerId: string): Promise<ProjectRecord[]> {
  const rows = await db
    .select()
    .from(projects)
    .where(eq(projects.customerId, customerId))
    .orderBy(asc(projects.createdAt), asc(projects.id));
  return rows.map((row) => ({
    id: row.id,
    customerId: row.customerId,
    name: row.name,
    createdAt: row.createdAt,
  }));
}

export async function getProjectById(db: Db, projectId: string): Promise<ProjectRecord | null> {
  const rows = await db.select().from(projects).where(eq(projects.id, projectId)).limit(1);
  const row = rows[0];
  if (!row) return null;
  return {
    id: row.id,
    customerId: row.customerId,
    name: row.name,
    createdAt: row.createdAt,
  };
}

export async function ensureDefaultProject(db: Db, customerId: string): Promise<ProjectRecord> {
  const existing = await listProjectsForCustomer(db, customerId);
  if (existing.length > 0) return existing[0]!;

  const insertedId = `proj_${randomHex16()}`;
  const [row] = await db
    .insert(projects)
    .values({
      id: insertedId,
      customerId,
      name: "Default project",
      createdAt: new Date(),
    })
    .returning();
  if (!row) throw new Error("ensureDefaultProject: no row returned");
  return {
    id: row.id,
    customerId: row.customerId,
    name: row.name,
    createdAt: row.createdAt,
  };
}

export async function customerProjectIds(db: Db, customerId: string): Promise<Set<string>> {
  const rows = await listProjectsForCustomer(db, customerId);
  return new Set([customerId, ...rows.map((row) => row.id)]);
}

function randomHex16(): string {
  return Math.random().toString(16).slice(2, 18).padEnd(16, "0");
}
