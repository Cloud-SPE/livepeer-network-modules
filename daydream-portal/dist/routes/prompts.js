import { z } from "zod";
import { createPrompt, deletePrompt, listForCustomer, updatePrompt, } from "../repo/savedPrompts.js";
import { customerAuthPreHandler } from "../middleware/auth.js";
const CreateSchema = z.object({
    label: z.string().trim().min(1).max(120),
    body: z.string().trim().min(1).max(8_000),
});
const UpdateSchema = z.object({
    label: z.string().trim().min(1).max(120).optional(),
    body: z.string().trim().min(1).max(8_000).optional(),
});
export function registerPromptRoutes(app, deps) {
    const requireCustomer = customerAuthPreHandler(deps.portal.customerTokenService);
    app.get("/portal/prompts", { preHandler: requireCustomer }, async (req, reply) => {
        const rows = await listForCustomer(deps.db, req.customerSession.customer.id);
        await reply.send({
            prompts: rows.map((r) => ({
                id: r.id,
                label: r.label,
                body: r.body,
                created_at: r.createdAt.toISOString(),
                updated_at: r.updatedAt.toISOString(),
            })),
        });
    });
    app.post("/portal/prompts", { preHandler: requireCustomer }, async (req, reply) => {
        const parsed = CreateSchema.safeParse(req.body);
        if (!parsed.success) {
            await reply
                .code(400)
                .send({ error: "invalid_request", details: parsed.error.flatten() });
            return;
        }
        const row = await createPrompt(deps.db, {
            customerId: req.customerSession.customer.id,
            label: parsed.data.label,
            body: parsed.data.body,
        });
        await reply.code(201).send({ id: row.id });
    });
    app.patch("/portal/prompts/:id", { preHandler: requireCustomer }, async (req, reply) => {
        const parsed = UpdateSchema.safeParse(req.body);
        if (!parsed.success) {
            await reply
                .code(400)
                .send({ error: "invalid_request", details: parsed.error.flatten() });
            return;
        }
        const row = await updatePrompt(deps.db, {
            id: req.params.id,
            customerId: req.customerSession.customer.id,
            label: parsed.data.label,
            body: parsed.data.body,
        });
        if (!row) {
            await reply.code(404).send({ error: "not_found" });
            return;
        }
        await reply.send({ id: row.id });
    });
    app.delete("/portal/prompts/:id", { preHandler: requireCustomer }, async (req, reply) => {
        const ok = await deletePrompt(deps.db, {
            id: req.params.id,
            customerId: req.customerSession.customer.id,
        });
        if (!ok) {
            await reply.code(404).send({ error: "not_found" });
            return;
        }
        await reply.code(204).send();
    });
}
//# sourceMappingURL=prompts.js.map