import { randomUUID } from "node:crypto";
import type { FastifyInstance, FastifyPluginAsync } from "fastify";
import { eq } from "drizzle-orm";
import { z } from "zod";

import { authPreHandler } from "@livepeer-rewrite/customer-portal/middleware";

import {
  SELECTOR_HEADER,
} from "../providers/serviceRegistry.js";
import type { VtuberGatewayDeps } from "../runtime/deps.js";
import {
  hashSessionBearer,
  mintSessionBearer,
} from "../service/auth/sessionBearer.js";
import { mintWorkerControlBearer } from "../service/auth/workerControlBearer.js";
import {
  SessionEndRequestSchema,
  SessionOpenRequestSchema,
  SessionTopupRequestSchema,
  type SessionOpenRequest,
} from "../types/vtuber.js";
import { vtuberNodeHealth, vtuberRateCardSession, vtuberUsageRecord } from "../repo/schema.js";

export const SessionIdParamsSchema = z.object({
  id: z.string().uuid(),
});

const VTUBER_CAPABILITY = "livepeer:vtuber-session";

export interface SessionsRouteDeps {
  deps: VtuberGatewayDeps;
}

export const registerSessionsRoutes: FastifyPluginAsync<SessionsRouteDeps> =
  async (app: FastifyInstance, { deps }: SessionsRouteDeps) => {
    const { cfg } = deps;

    app.post(
      "/v1/vtuber/sessions",
      { preHandler: authPreHandler(deps.authResolver) },
      async (req, reply) => {
        const parsed = SessionOpenRequestSchema.safeParse(req.body);
        if (!parsed.success) {
          await reply
            .code(400)
            .send({ error: "invalid_request", details: parsed.error.issues });
          return;
        }
        const body: SessionOpenRequest = parsed.data;
        const offering = body.offering ?? cfg.vtuberDefaultOffering;
        const customerId = req.caller?.id;
        if (typeof customerId !== "string") {
          await reply.code(401).send({ error: "authentication_required" });
          return;
        }

        const node = await deps.serviceRegistry.select({
          capability: VTUBER_CAPABILITY,
          offering,
          extra: parseJsonHeader(req.headers[SELECTOR_HEADER.EXTRA]),
          constraints: parseJsonHeader(
            req.headers[SELECTOR_HEADER.CONSTRAINTS],
          ),
          maxPricePerUnitWei: parseStringHeader(
            req.headers[SELECTOR_HEADER.MAX_PRICE_WEI],
          ),
        });
        if (node === null) {
          await reply
            .code(503)
            .header("Retry-After", "5")
            .header("Livepeer-Error", "no_worker_available")
            .send({ error: "no_worker_available" });
          return;
        }

        const sessionId = randomUUID();
        const ttlSeconds =
          body.ttl_seconds ?? cfg.vtuberSessionDefaultTtlSeconds;
        const expiresAt = new Date(Date.now() + ttlSeconds * 1000);

        const minted = mintSessionBearer(cfg.vtuberSessionBearerPepper);
        const workerBearer = mintWorkerControlBearer(
          cfg.vtuberWorkerControlBearerPepper,
        );

        await deps.sessionStore.insertSession({
          id: sessionId,
          customerId,
          paramsJson: JSON.stringify(body),
          nodeId: node.nodeId,
          nodeUrl: node.nodeUrl,
          controlUrl: "",
          expiresAt,
        });
        await deps.sessionStore.insertBearer({
          sessionId,
          customerId,
          hash: minted.hash,
        });

        let payment;
        try {
          payment = await deps.payerDaemon.createPayment({
            faceValueWei: cfg.payerDefaultFaceValueWei,
            recipientEthAddress: node.ethAddress,
            capability: VTUBER_CAPABILITY,
            offering,
            nodeId: node.nodeId,
          });
        } catch (err) {
          await deps.sessionStore.updateSession(sessionId, {
            status: "errored",
            errorCode: "payment_emit_failed",
          });
          req.log.error({ err, sessionId }, "payerDaemon.createPayment failed");
          await reply
            .code(502)
            .header("Livepeer-Error", "payment_emit_failed")
            .send({ error: "payment_emit_failed" });
          return;
        }

        await deps.sessionStore.updateSession(sessionId, {
          payerWorkId: payment.payerWorkId,
        });

        try {
          const startResp = await deps.worker.startSession(node.nodeUrl, {
            request: {
              session_id: sessionId,
              persona: body.persona,
              vrm_url: body.vrm_url,
              llm_provider: body.llm_provider,
              tts_provider: body.tts_provider,
              ...(body.target_youtube_broadcast !== undefined
                ? { target_youtube_broadcast: body.target_youtube_broadcast }
                : {}),
              width: body.width,
              height: body.height,
              target_fps: body.target_fps,
              worker_control_bearer: workerBearer.bearer,
            },
            paymentHeader: payment.paymentHeader,
            offering,
          });
          const controlUrl = startResp.control_url ?? buildControlUrl(req, sessionId);
          await deps.sessionStore.updateSession(sessionId, {
            status: "active",
            workerSessionId: startResp.session_id,
            controlUrl,
          });
          await markNodeSuccess(deps, node.nodeId, node.nodeUrl);
          await reply.code(200).send({
            session_id: sessionId,
            control_url: controlUrl,
            expires_at: expiresAt.toISOString(),
            session_child_bearer: minted.bearer,
          });
          return;
        } catch (err) {
          await deps.sessionStore.updateSession(sessionId, {
            status: "errored",
            errorCode: "worker_start_failed",
          });
          await markNodeFailure(deps, node.nodeId, node.nodeUrl);
          req.log.error({ err, sessionId }, "workerClient.startSession failed");
          await reply
            .code(502)
            .header("Livepeer-Error", "worker_start_failed")
            .send({ error: "worker_start_failed" });
          return;
        }
      },
    );

    app.get<{ Params: { id: string } }>(
      "/v1/vtuber/sessions/:id",
      { preHandler: sessionBearerPreHandler(deps) },
      async (req, reply) => {
        const parsed = SessionIdParamsSchema.safeParse(req.params);
        if (!parsed.success) {
          await reply.code(400).send({ error: "invalid_session_id" });
          return;
        }
        const session = req.vtuberSession;
        if (session === undefined || session.id !== parsed.data.id) {
          await reply.code(404).send({ error: "session_not_found" });
          return;
        }
        await reply.code(200).send({
          session_id: session.id,
          status: session.status,
          error_code: session.errorCode,
          expires_at: session.expiresAt.toISOString(),
          ended_at: session.endedAt?.toISOString() ?? null,
        });
      },
    );

    app.post<{ Params: { id: string }; Body: unknown }>(
      "/v1/vtuber/sessions/:id/end",
      { preHandler: sessionBearerPreHandler(deps) },
      async (req, reply) => {
        const parsedId = SessionIdParamsSchema.safeParse(req.params);
        if (!parsedId.success) {
          await reply.code(400).send({ error: "invalid_session_id" });
          return;
        }
        const parsedBody = SessionEndRequestSchema.safeParse(req.body ?? {});
        if (!parsedBody.success) {
          await reply.code(400).send({ error: "invalid_end_body" });
          return;
        }
        const session = req.vtuberSession;
        if (session === undefined || session.id !== parsedId.data.id) {
          await reply.code(404).send({ error: "session_not_found" });
          return;
        }

        if (
          session.workerSessionId !== null &&
          session.nodeUrl !== null
        ) {
          try {
            await deps.worker.stopSession(session.nodeUrl, session.workerSessionId);
            if (session.nodeId !== null) {
              await markNodeSuccess(deps, session.nodeId, session.nodeUrl);
            }
          } catch (err) {
            if (session.nodeId !== null) {
              await markNodeFailure(deps, session.nodeId, session.nodeUrl);
            }
            req.log.warn(
              { err, sessionId: session.id },
              "worker.stopSession best-effort failed",
            );
          }
        }
        const endedAt = new Date();
        const wasAlreadyEnded = session.endedAt !== null || session.status === "ended";
        await deps.sessionStore.updateSession(session.id, {
          status: "ended",
          endedAt,
        });
        if (!wasAlreadyEnded) {
          await recordUsageForEndedSession(deps, session, endedAt);
        }

        await reply.code(200).send({
          session_id: session.id,
          status: "ended",
          ended_at: endedAt.toISOString(),
        });
      },
    );

    app.post<{ Params: { id: string }; Body: unknown }>(
      "/v1/vtuber/sessions/:id/topup",
      { preHandler: sessionBearerPreHandler(deps) },
      async (req, reply) => {
        const parsedId = SessionIdParamsSchema.safeParse(req.params);
        if (!parsedId.success) {
          await reply.code(400).send({ error: "invalid_session_id" });
          return;
        }
        const parsedBody = SessionTopupRequestSchema.safeParse(req.body);
        if (!parsedBody.success) {
          await reply.code(400).send({
            error: "invalid_topup_body",
            details: parsedBody.error.issues,
          });
          return;
        }
        const session = req.vtuberSession;
        if (session === undefined || session.id !== parsedId.data.id) {
          await reply.code(404).send({ error: "session_not_found" });
          return;
        }
        if (
          session.nodeId === null ||
          session.nodeUrl === null ||
          session.workerSessionId === null
        ) {
          await reply.code(409).send({ error: "session_not_active" });
          return;
        }
        const node = await deps.serviceRegistry.getNode(session.nodeId);
        if (node === null) {
          await reply
            .code(503)
            .header("Livepeer-Error", "no_worker_available")
            .send({ error: "no_worker_available" });
          return;
        }
        const offering =
          (JSON.parse(session.paramsJson).offering as string | undefined) ??
          cfg.vtuberDefaultOffering;
        let payment;
        try {
          payment = await deps.payerDaemon.createPayment({
            faceValueWei: cfg.payerDefaultFaceValueWei,
            recipientEthAddress: node.ethAddress,
            capability: VTUBER_CAPABILITY,
            offering,
            nodeId: node.nodeId,
          });
        } catch (err) {
          req.log.error({ err, sessionId: session.id }, "topup payment failed");
          await reply
            .code(502)
            .header("Livepeer-Error", "payment_emit_failed")
            .send({ error: "payment_emit_failed" });
          return;
        }
        try {
          await deps.worker.topupSession(session.nodeUrl, {
            sessionId: session.workerSessionId,
            paymentHeader: payment.paymentHeader,
          });
          await markNodeSuccess(deps, node.nodeId, session.nodeUrl);
        } catch (err) {
          await markNodeFailure(deps, node.nodeId, session.nodeUrl);
          req.log.error(
            { err, sessionId: session.id },
            "worker.topupSession failed",
          );
          await reply
            .code(502)
            .header("Livepeer-Error", "worker_topup_failed")
            .send({ error: "worker_topup_failed" });
          return;
        }
        await reply.code(200).send({
          session_id: session.id,
          face_value_wei: cfg.payerDefaultFaceValueWei,
          payer_work_id: payment.payerWorkId,
        });
      },
    );
  };

function buildControlUrl(
  req: { protocol: string; hostname: string; headers: Record<string, unknown> },
  sessionId: string,
): string {
  const proto = req.protocol === "https" ? "wss" : "ws";
  const host =
    typeof req.headers["host"] === "string" ? (req.headers["host"] as string) : req.hostname;
  return `${proto}://${host}/v1/vtuber/sessions/${sessionId}/control`;
}

declare module "fastify" {
  interface FastifyRequest {
    vtuberSession?: import("../service/sessions/sessionStore.js").VtuberSessionRecord;
  }
}

import type { preHandlerAsyncHookHandler } from "fastify";

export function sessionBearerPreHandler(
  deps: VtuberGatewayDeps,
): preHandlerAsyncHookHandler {
  return async (req, reply) => {
    const auth = req.headers["authorization"];
    if (typeof auth !== "string" || !auth.startsWith("Bearer ")) {
      await reply
        .code(401)
        .send({ error: "missing_session_bearer" });
      return;
    }
    const bearer = auth.slice("Bearer ".length).trim();
    if (!bearer.startsWith("vtbs_")) {
      await reply.code(401).send({ error: "invalid_session_bearer" });
      return;
    }
    let hash: string;
    try {
      hash = hashSessionBearer(bearer, deps.cfg.vtuberSessionBearerPepper);
    } catch {
      await reply.code(401).send({ error: "invalid_session_bearer" });
      return;
    }
    const session = await deps.sessionStore.findByBearerHash(hash);
    if (session === null) {
      await reply.code(401).send({ error: "invalid_session_bearer" });
      return;
    }
    req.vtuberSession = session;
  };
}

function parseJsonHeader(value: string | string[] | undefined): any {
  const raw = Array.isArray(value) ? value[0] : value;
  if (!raw) return null;
  return JSON.parse(raw);
}

function parseStringHeader(value: string | string[] | undefined): string | null {
  const raw = Array.isArray(value) ? value[0] : value;
  return raw && raw !== "" ? raw : null;
}

async function recordUsageForEndedSession(
  deps: VtuberGatewayDeps,
  session: import("../service/sessions/sessionStore.js").VtuberSessionRecord,
  endedAt: Date,
): Promise<void> {
  if (!deps.vtuberDb) {
    return;
  }
  const elapsedMs = Math.max(0, endedAt.getTime() - session.createdAt.getTime());
  const seconds = Math.max(1, Math.ceil(elapsedMs / 1000));
  const offering = readOffering(session.paramsJson) ?? "default";
  const cents = await computeSessionCents(deps, offering, seconds);
  await deps.vtuberDb.insert(vtuberUsageRecord).values({
    id: randomUUID(),
    sessionId: session.id,
    customerId: session.customerId,
    seconds,
    cents,
    createdAt: endedAt,
  });
}

async function computeSessionCents(
  deps: VtuberGatewayDeps,
  offering: string,
  seconds: number,
): Promise<bigint> {
  if (!deps.vtuberDb) {
    return BigInt(Math.max(1, Math.ceil(seconds * parseFloat(deps.cfg.vtuberRateCardUsdPerSecond) * 100)));
  }
  const rateRows = await deps.vtuberDb
    .select({ usdPerSecond: vtuberRateCardSession.usdPerSecond })
    .from(vtuberRateCardSession)
    .where(eq(vtuberRateCardSession.offering, offering))
    .limit(1);
  const configuredRate =
    rateRows[0]?.usdPerSecond ?? deps.cfg.vtuberRateCardUsdPerSecond;
  return BigInt(
    Math.max(1, Math.ceil(seconds * parseFloat(configuredRate) * 100)),
  );
}

function readOffering(paramsJson: string): string | null {
  try {
    const parsed = JSON.parse(paramsJson) as Record<string, unknown>;
    return typeof parsed["offering"] === "string" ? parsed["offering"] : null;
  } catch {
    return null;
  }
}

async function markNodeSuccess(
  deps: VtuberGatewayDeps,
  nodeId: string,
  nodeUrl: string,
): Promise<void> {
  if (!deps.vtuberDb) {
    return;
  }
  const now = new Date();
  await deps.vtuberDb
    .insert(vtuberNodeHealth)
    .values({
      nodeId,
      nodeUrl,
      lastSuccessAt: now,
      lastFailureAt: null,
      consecutiveFails: 0,
      circuitOpen: false,
      updatedAt: now,
    })
    .onConflictDoUpdate({
      target: vtuberNodeHealth.nodeId,
      set: {
        nodeUrl,
        lastSuccessAt: now,
        consecutiveFails: 0,
        circuitOpen: false,
        updatedAt: now,
      },
    });
}

async function markNodeFailure(
  deps: VtuberGatewayDeps,
  nodeId: string,
  nodeUrl: string,
): Promise<void> {
  if (!deps.vtuberDb) {
    return;
  }
  const existing = await deps.vtuberDb
    .select()
    .from(vtuberNodeHealth)
    .where(eq(vtuberNodeHealth.nodeId, nodeId))
    .limit(1);
  const priorFails = existing[0]?.consecutiveFails ?? 0;
  const nextFails = priorFails + 1;
  const now = new Date();
  await deps.vtuberDb
    .insert(vtuberNodeHealth)
    .values({
      nodeId,
      nodeUrl,
      lastSuccessAt: existing[0]?.lastSuccessAt ?? null,
      lastFailureAt: now,
      consecutiveFails: nextFails,
      circuitOpen: nextFails >= 3,
      updatedAt: now,
    })
    .onConflictDoUpdate({
      target: vtuberNodeHealth.nodeId,
      set: {
        nodeUrl,
        lastFailureAt: now,
        consecutiveFails: nextFails,
        circuitOpen: nextFails >= 3,
        updatedAt: now,
      },
    });
}
