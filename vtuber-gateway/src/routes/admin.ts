import { desc } from "drizzle-orm";
import type { FastifyInstance, FastifyReply, FastifyRequest, preHandlerAsyncHookHandler } from "fastify";
import type { AdminAuthResolver } from "@livepeer-network-modules/customer-portal/auth";

import type { Db } from "../db/pool.js";
import { vtuberNodeHealth, vtuberRateCardSession, vtuberUsageRecord } from "../repo/schema.js";
import type { ServiceRegistryClient } from "../providers/serviceRegistry.js";
import type { SessionStore } from "../service/sessions/sessionStore.js";

declare module "fastify" {
  interface FastifyRequest {
    adminActor?: string;
  }
}

export interface AdminRoutesDeps {
  authResolver: AdminAuthResolver;
  db: Db;
  sessionStore: SessionStore;
  serviceRegistry: ServiceRegistryClient;
  vtuberRateCardUsdPerSecond: string;
}

export function registerAdmin(app: FastifyInstance, deps: AdminRoutesDeps): void {
  const preHandler = adminAuthPreHandler(deps.authResolver);

  app.get("/admin/vtuber/sessions", { preHandler }, async (_req, reply) => {
    const rows = await deps.sessionStore.listSessions({ limit: 100 });
    await reply.code(200).send({
      sessions: rows.map((row) => {
        const params = safeParseJson(row.paramsJson);
        return {
          id: row.id,
          customer_id: row.customerId,
          status: row.status,
          persona: typeof params.persona === "string" ? params.persona : null,
          llm_provider: typeof params.llm_provider === "string" ? params.llm_provider : null,
          tts_provider: typeof params.tts_provider === "string" ? params.tts_provider : null,
          node_id: row.nodeId,
          node_url: row.nodeUrl,
          payer_work_id: row.payerWorkId,
          created_at: row.createdAt.toISOString(),
          expires_at: row.expiresAt.toISOString(),
          ended_at: row.endedAt?.toISOString() ?? null,
          error_code: row.errorCode,
        };
      }),
    });
  });

  app.get("/admin/vtuber/usage", { preHandler }, async (_req, reply) => {
    const rows = await deps.db
      .select()
      .from(vtuberUsageRecord)
      .orderBy(desc(vtuberUsageRecord.createdAt))
      .limit(200);
    await reply.code(200).send({
      usage: rows.map((row) => ({
        id: row.id,
        session_id: row.sessionId,
        customer_id: row.customerId,
        seconds: row.seconds,
        cents: row.cents.toString(),
        created_at: row.createdAt.toISOString(),
      })),
    });
  });

  app.get("/admin/vtuber/node-health", { preHandler }, async (_req, reply) => {
    const storedRows = await deps.db
      .select()
      .from(vtuberNodeHealth)
      .orderBy(desc(vtuberNodeHealth.updatedAt))
      .limit(100);
    const rows =
      storedRows.length > 0
        ? storedRows
        : (await deps.serviceRegistry.listVtuberNodes()).map((node) => ({
            nodeId: node.nodeId,
            nodeUrl: node.nodeUrl,
            lastSuccessAt: null,
            lastFailureAt: null,
            consecutiveFails: 0,
            circuitOpen: false,
            updatedAt: new Date(0),
          }));
    await reply.code(200).send({
      nodes: rows.map((row) => ({
        node_id: row.nodeId,
        node_url: row.nodeUrl,
        last_success_at: row.lastSuccessAt?.toISOString() ?? null,
        last_failure_at: row.lastFailureAt?.toISOString() ?? null,
        consecutive_fails: row.consecutiveFails,
        circuit_open: row.circuitOpen,
        updated_at: row.updatedAt.toISOString(),
      })),
    });
  });

  app.get("/admin/vtuber/rate-card", { preHandler }, async (_req, reply) => {
    const rows = await deps.db
      .select()
      .from(vtuberRateCardSession)
      .orderBy(desc(vtuberRateCardSession.updatedAt))
      .limit(50);
    await reply.code(200).send({
      rate_card: rows.length > 0
        ? rows.map((row) => ({
            offering: row.offering,
            usd_per_second: row.usdPerSecond,
            created_at: row.createdAt.toISOString(),
            updated_at: row.updatedAt.toISOString(),
          }))
        : [
            {
              offering: "default",
              usd_per_second: deps.vtuberRateCardUsdPerSecond,
              created_at: null,
              updated_at: null,
            },
          ],
    });
  });
}

function adminAuthPreHandler(
  resolver: AdminAuthResolver,
): preHandlerAsyncHookHandler {
  return async (req: FastifyRequest, reply: FastifyReply): Promise<void> => {
    const result = await resolver.resolve({
      headers: req.headers as Record<string, string | undefined>,
      ip: req.ip,
    });
    if (!result) {
      await reply.code(401).send({
        error: {
          code: "authentication_failed",
          message: "admin token + actor required",
          type: "AdminAuthError",
        },
      });
      return;
    }
    req.adminActor = result.actor;
  };
}

function safeParseJson(value: string): Record<string, unknown> {
  try {
    return JSON.parse(value) as Record<string, unknown>;
  } catch {
    return {};
  }
}
