import { asc, eq, sql } from "drizzle-orm";

import type { Db } from "../db/pool.js";
import { assets, liveStreams, projects, uploads, webhookEndpoints } from "../db/schema.js";

export interface ProjectRecord {
  id: string;
  customerId: string;
  name: string;
  createdAt: Date;
}

export interface ProjectUsageSummary {
  assets: number;
  uploads: number;
  liveStreams: number;
  webhooks: number;
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

export async function createProject(
  db: Db,
  input: { customerId: string; name: string },
): Promise<ProjectRecord> {
  const id = `proj_${randomHex16()}`;
  const [row] = await db
    .insert(projects)
    .values({
      id,
      customerId: input.customerId,
      name: input.name,
      createdAt: new Date(),
    })
    .returning();
  if (!row) throw new Error("createProject: no row returned");
  return {
    id: row.id,
    customerId: row.customerId,
    name: row.name,
    createdAt: row.createdAt,
  };
}

export async function renameProject(
  db: Db,
  input: { projectId: string; name: string },
): Promise<ProjectRecord | null> {
  const rows = await db
    .update(projects)
    .set({ name: input.name })
    .where(eq(projects.id, input.projectId))
    .returning();
  const row = rows[0];
  if (!row) return null;
  return {
    id: row.id,
    customerId: row.customerId,
    name: row.name,
    createdAt: row.createdAt,
  };
}

export async function summarizeProjectUsage(
  db: Db,
  projectId: string,
): Promise<ProjectUsageSummary> {
  const [assetRow, uploadRow, liveRow, webhookRow] = await Promise.all([
    db.select({ count: sql<number>`count(*)::int` }).from(assets).where(eq(assets.projectId, projectId)),
    db.select({ count: sql<number>`count(*)::int` }).from(uploads).where(eq(uploads.projectId, projectId)),
    db.select({ count: sql<number>`count(*)::int` }).from(liveStreams).where(eq(liveStreams.projectId, projectId)),
    db.select({ count: sql<number>`count(*)::int` }).from(webhookEndpoints).where(eq(webhookEndpoints.projectId, projectId)),
  ]);
  return {
    assets: assetRow[0]?.count ?? 0,
    uploads: uploadRow[0]?.count ?? 0,
    liveStreams: liveRow[0]?.count ?? 0,
    webhooks: webhookRow[0]?.count ?? 0,
  };
}

export async function deleteProject(db: Db, projectId: string): Promise<boolean> {
  const rows = await db.delete(projects).where(eq(projects.id, projectId)).returning({ id: projects.id });
  return rows.length > 0;
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
