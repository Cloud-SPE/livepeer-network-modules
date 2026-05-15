import { and, desc, eq } from "drizzle-orm";
import type { NodePgDatabase } from "drizzle-orm/node-postgres";
import { savedPrompts } from "../db/schema.js";

export type PromptDb = NodePgDatabase<Record<string, unknown>>;

export interface SavedPromptRow {
  id: string;
  customerId: string;
  label: string;
  body: string;
  createdAt: Date;
  updatedAt: Date;
}

export async function listForCustomer(
  db: PromptDb,
  customerId: string,
): Promise<SavedPromptRow[]> {
  const rows = await db
    .select()
    .from(savedPrompts)
    .where(eq(savedPrompts.customerId, customerId))
    .orderBy(desc(savedPrompts.updatedAt));
  return rows as SavedPromptRow[];
}

export async function createPrompt(
  db: PromptDb,
  input: { customerId: string; label: string; body: string },
): Promise<SavedPromptRow> {
  const [row] = await db
    .insert(savedPrompts)
    .values({
      customerId: input.customerId,
      label: input.label,
      body: input.body,
    })
    .returning();
  return row as SavedPromptRow;
}

export async function updatePrompt(
  db: PromptDb,
  input: { id: string; customerId: string; label?: string; body?: string },
): Promise<SavedPromptRow | null> {
  const patch: Record<string, unknown> = { updatedAt: new Date() };
  if (input.label !== undefined) patch.label = input.label;
  if (input.body !== undefined) patch.body = input.body;
  const [row] = await db
    .update(savedPrompts)
    .set(patch)
    .where(
      and(
        eq(savedPrompts.id, input.id),
        eq(savedPrompts.customerId, input.customerId),
      ),
    )
    .returning();
  return (row as SavedPromptRow | undefined) ?? null;
}

export async function deletePrompt(
  db: PromptDb,
  input: { id: string; customerId: string },
): Promise<boolean> {
  const rows = await db
    .delete(savedPrompts)
    .where(
      and(
        eq(savedPrompts.id, input.id),
        eq(savedPrompts.customerId, input.customerId),
      ),
    )
    .returning({ id: savedPrompts.id });
  return rows.length > 0;
}
