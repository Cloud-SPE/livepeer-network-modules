import { and, desc, eq } from "drizzle-orm";
import { savedPrompts } from "../db/schema.js";
export async function listForCustomer(db, customerId) {
    const rows = await db
        .select()
        .from(savedPrompts)
        .where(eq(savedPrompts.customerId, customerId))
        .orderBy(desc(savedPrompts.updatedAt));
    return rows;
}
export async function createPrompt(db, input) {
    const [row] = await db
        .insert(savedPrompts)
        .values({
        customerId: input.customerId,
        label: input.label,
        body: input.body,
    })
        .returning();
    return row;
}
export async function updatePrompt(db, input) {
    const patch = { updatedAt: new Date() };
    if (input.label !== undefined)
        patch.label = input.label;
    if (input.body !== undefined)
        patch.body = input.body;
    const [row] = await db
        .update(savedPrompts)
        .set(patch)
        .where(and(eq(savedPrompts.id, input.id), eq(savedPrompts.customerId, input.customerId)))
        .returning();
    return row ?? null;
}
export async function deletePrompt(db, input) {
    const rows = await db
        .delete(savedPrompts)
        .where(and(eq(savedPrompts.id, input.id), eq(savedPrompts.customerId, input.customerId)))
        .returning({ id: savedPrompts.id });
    return rows.length > 0;
}
//# sourceMappingURL=savedPrompts.js.map