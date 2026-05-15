// Daydream admin surface. Stays deliberately tiny (blueclaw-scope):
// waitlist queue with approve/reject, customer list, usage rollup.
// Heavier admin ops (balance adjust, status, refund) live in
// customer-portal's adminEngine — exposed wholesale via its own helper,
// not duplicated here.

import type { FastifyInstance } from "fastify";
import { z } from "zod";
import type { NodePgDatabase } from "drizzle-orm/node-postgres";
import type {
  CustomerPortal,
  auth,
} from "@livepeer-network-modules/customer-portal";
import {
  getWaitlistById,
  listWaitlist,
  setWaitlistDecision,
} from "../repo/waitlist.js";
import { adminSummary } from "../repo/usage.js";
import { adminAuthPreHandler } from "../middleware/auth.js";

const ApproveSchema = z.object({
  // Optional label for the API key that gets issued on approval.
  // Defaults to the waitlist row's display_name or email local-part.
  key_label: z.string().trim().min(1).max(120).optional(),
});

const RejectSchema = z.object({
  reason: z.string().trim().min(1).max(2_000).optional(),
});

const ListQuerySchema = z.object({
  status: z.enum(["pending", "approved", "rejected"]).optional(),
  limit: z.coerce.number().int().min(1).max(200).optional(),
  offset: z.coerce.number().int().min(0).optional(),
});

const THIRTY_DAYS_MS = 30 * 24 * 60 * 60 * 1_000;

export interface RegisterAdminRoutesDeps {
  db: NodePgDatabase<Record<string, unknown>>;
  portal: CustomerPortal;
  adminAuthResolver: auth.AdminAuthResolver;
}

export function registerDaydreamAdminRoutes(
  app: FastifyInstance,
  deps: RegisterAdminRoutesDeps,
): void {
  const requireAdmin = adminAuthPreHandler(deps.adminAuthResolver);

  app.get(
    "/admin/waitlist",
    { preHandler: requireAdmin },
    async (req, reply) => {
      const parsed = ListQuerySchema.safeParse(req.query);
      if (!parsed.success) {
        await reply
          .code(400)
          .send({ error: "invalid_request", details: parsed.error.flatten() });
        return;
      }
      const rows = await listWaitlist(deps.db, parsed.data);
      await reply.send({
        entries: rows.map((r) => ({
          id: r.id,
          email: r.email,
          display_name: r.displayName,
          reason: r.reason,
          status: r.status,
          customer_id: r.customerId,
          decided_by: r.decidedBy,
          decided_at: r.decidedAt?.toISOString() ?? null,
          created_at: r.createdAt.toISOString(),
        })),
      });
    },
  );

  app.post<{ Params: { id: string } }>(
    "/admin/waitlist/:id/approve",
    { preHandler: requireAdmin },
    async (req, reply) => {
      const parsed = ApproveSchema.safeParse(req.body ?? {});
      if (!parsed.success) {
        await reply
          .code(400)
          .send({ error: "invalid_request", details: parsed.error.flatten() });
        return;
      }
      const row = await getWaitlistById(deps.db, req.params.id);
      if (!row) {
        await reply.code(404).send({ error: "not_found" });
        return;
      }
      if (row.status !== "pending") {
        await reply
          .code(409)
          .send({ error: "already_decided", status: row.status });
        return;
      }
      const customer = await deps.portal.adminEngine.createCustomer({
        email: row.email,
        actor: req.adminActor!,
      });
      const issued = await deps.portal.issueApiKey({
        customerId: customer.id,
        label:
          parsed.data.key_label ??
          row.displayName ??
          row.email.split("@")[0] ??
          "daydream-portal",
      });
      const updated = await setWaitlistDecision(deps.db, {
        id: row.id,
        status: "approved",
        decidedBy: req.adminActor!,
        customerId: customer.id,
      });
      if (!updated) {
        // Lost a race; another admin approved in parallel. The
        // customer + key still got created, so surface that fact
        // honestly rather than pretending nothing happened.
        req.log.warn(
          { id: row.id, customerId: customer.id },
          "waitlist row state changed during approve",
        );
      }
      await reply.send({
        waitlist_id: row.id,
        customer_id: customer.id,
        api_key_id: issued.apiKeyId,
        api_key: issued.plaintext,
        warning: "api_key shown once; copy and share out-of-band",
      });
    },
  );

  app.post<{ Params: { id: string } }>(
    "/admin/waitlist/:id/reject",
    { preHandler: requireAdmin },
    async (req, reply) => {
      const parsed = RejectSchema.safeParse(req.body ?? {});
      if (!parsed.success) {
        await reply
          .code(400)
          .send({ error: "invalid_request", details: parsed.error.flatten() });
        return;
      }
      const updated = await setWaitlistDecision(deps.db, {
        id: req.params.id,
        status: "rejected",
        decidedBy: req.adminActor!,
      });
      if (!updated) {
        await reply.code(404).send({ error: "not_found_or_decided" });
        return;
      }
      await reply.send({ waitlist_id: updated.id, status: updated.status });
    },
  );

  app.get(
    "/admin/usage/summary",
    { preHandler: requireAdmin },
    async (_req, reply) => {
      const windowStart = new Date(Date.now() - THIRTY_DAYS_MS);
      const summary = await adminSummary(deps.db, windowStart);
      await reply.send({
        window_days: 30,
        total_sessions: summary.totalSessions,
        total_seconds: summary.totalSeconds,
        unique_customers: summary.uniqueCustomers,
      });
    },
  );
}
