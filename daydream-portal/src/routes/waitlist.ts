import type { FastifyInstance } from "fastify";
import { z } from "zod";
import type { NodePgDatabase } from "drizzle-orm/node-postgres";
import {
  createWaitlistEntry,
  getWaitlistByEmail,
} from "../repo/waitlist.js";

const SignupSchema = z.object({
  email: z.string().email().max(320),
  display_name: z.string().trim().min(1).max(120).optional(),
  reason: z.string().trim().max(2_000).optional(),
});

export interface RegisterWaitlistRoutesDeps {
  db: NodePgDatabase<Record<string, unknown>>;
}

// Public, unauthenticated. The signup form on the portal POSTs here.
// The handler is deliberately thin: no email verification, no key
// issuance — admin approval flow lives in routes/admin.ts.
export function registerWaitlistRoutes(
  app: FastifyInstance,
  deps: RegisterWaitlistRoutesDeps,
): void {
  app.post("/portal/waitlist", async (req, reply) => {
    const parsed = SignupSchema.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({
        error: "invalid_request",
        details: parsed.error.flatten(),
      });
      return;
    }
    const row = await createWaitlistEntry(deps.db, {
      email: parsed.data.email.toLowerCase(),
      displayName: parsed.data.display_name,
      reason: parsed.data.reason,
    });
    await reply.code(202).send({
      waitlist_id: row.id,
      email: row.email,
      status: row.status,
      created_at: row.createdAt.toISOString(),
    });
  });

  // Status probe — primarily for the post-signup confirmation screen
  // on the portal to let the user know if they've been approved yet
  // without leaking other accounts' state.
  app.get<{ Querystring: { email?: string } }>(
    "/portal/waitlist/status",
    async (req, reply) => {
      const email = (req.query.email ?? "").toLowerCase().trim();
      if (!email) {
        await reply.code(400).send({ error: "missing_email" });
        return;
      }
      const row = await getWaitlistByEmail(deps.db, email);
      if (!row) {
        await reply.send({ status: "unknown" });
        return;
      }
      await reply.send({ status: row.status });
    },
  );
}
