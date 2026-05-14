/**
 * Livepeer-aware session lifecycle routes.
 *
 * POST   /v1/sessions             open a paid session on a randomly-
 *                                 selected orch; return { session_id,
 *                                 scope_url, control_url }.
 * POST   /v1/sessions/:id/topup   mint additional payment for an
 *                                 existing session.
 * POST   /v1/sessions/:id/close   graceful close.
 * GET    /v1/sessions/:id          state + remaining metadata.
 */

import type { FastifyInstance } from "fastify";

import type { Config } from "../config.js";
import { openControlWs } from "../controlWsClient.js";
import type { OrchSelector, OrchCandidate } from "../orchSelector.js";
import type { SessionRouter } from "../sessionRouter.js";
import { buildPayment } from "../paymentClient.js";

interface BrokerSessionOpenResponse {
  session_id: string;
  control_url: string;
  media: {
    schema: string;
    scope_url: string;
  };
  expires_at: string;
}

export function registerSessionRoutes(
  app: FastifyInstance,
  cfg: Config,
  selector: OrchSelector,
  router: SessionRouter,
): void {
  app.post("/v1/sessions", async (_req, reply) => {
    const maxAttempts = 3;
    let lastErr: unknown = null;
    for (let i = 0; i < maxAttempts; i++) {
      let orch: OrchCandidate;
      try {
        orch = await selector.pickRandom();
      } catch (e) {
        return reply.code(503).send({
          error: "no_orchs_available",
          message: (e as Error).message,
        });
      }

      try {
        const paymentHeader = await buildPayment({
          capabilityId: cfg.capabilityId,
          offeringId: cfg.offeringId,
          recipientHex: orch.ethAddress,
          brokerUrl: orch.brokerUrl,
        });

        const openUrl = stripTrailingSlash(orch.brokerUrl) + "/v1/cap";
        const res = await fetch(openUrl, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "Livepeer-Capability": cfg.capabilityId,
            "Livepeer-Offering": cfg.offeringId,
            "Livepeer-Payment": paymentHeader,
            "Livepeer-Spec-Version": "0.1",
            "Livepeer-Mode": cfg.interactionMode,
          },
          body: "{}",
        });
        if (!res.ok) {
          lastErr = new Error(
            `broker session-open failed: ${res.status} ${await res.text()}`,
          );
          continue;
        }
        const body = (await res.json()) as BrokerSessionOpenResponse;
        const sessionId = body.session_id;
        const controlWs = openControlWs({
          controlUrl: body.control_url,
          sessionId,
          logger: app.log,
          onEnded: () => router.remove(sessionId),
          onError: (reason) =>
            app.log.warn({ session_id: sessionId, reason }, "session error"),
        });
        router.add({
          sessionId,
          orch,
          scopeUrl: body.media.scope_url,
          controlUrl: body.control_url,
          expiresAt: body.expires_at,
          createdAt: Date.now(),
          controlWs,
        });
        return reply.code(201).send({
          session_id: body.session_id,
          scope_url: body.media.scope_url,
          control_url: body.control_url,
          expires_at: body.expires_at,
          orch: {
            eth_address: orch.ethAddress,
            broker_url: orch.brokerUrl,
          },
        });
      } catch (e) {
        lastErr = e;
        continue;
      }
    }
    return reply.code(502).send({
      error: "broker_session_open_failed",
      message: lastErr ? (lastErr as Error).message : "unknown",
    });
  });

  app.get("/v1/sessions/:id", async (req, reply) => {
    const id = (req.params as { id: string }).id;
    const rec = router.get(id);
    if (!rec) {
      return reply.code(404).send({ error: "not_found" });
    }
    return {
      session_id: rec.sessionId,
      scope_url: rec.scopeUrl,
      control_url: rec.controlUrl,
      expires_at: rec.expiresAt,
      created_at: new Date(rec.createdAt).toISOString(),
      orch: {
        eth_address: rec.orch.ethAddress,
        broker_url: rec.orch.brokerUrl,
      },
    };
  });

  app.post("/v1/sessions/:id/topup", async (req, reply) => {
    const id = (req.params as { id: string }).id;
    const rec = router.get(id);
    if (!rec) {
      return reply.code(404).send({ error: "not_found" });
    }
    try {
      const paymentHeader = await buildPayment({
        capabilityId: cfg.capabilityId,
        offeringId: cfg.offeringId,
        recipientHex: rec.orch.ethAddress,
        brokerUrl: rec.orch.brokerUrl,
      });
      if (rec.controlWs && rec.controlWs.isOpen()) {
        rec.controlWs.topup(paymentHeader);
      }
      return reply.code(202).send({ session_id: id, topup_minted: true });
    } catch (e) {
      return reply
        .code(502)
        .send({ error: "topup_failed", message: (e as Error).message });
    }
  });

  app.post("/v1/sessions/:id/close", async (req, reply) => {
    const id = (req.params as { id: string }).id;
    const rec = router.get(id);
    if (!rec) {
      return reply.code(404).send({ error: "not_found" });
    }
    if (rec.controlWs && rec.controlWs.isOpen()) {
      rec.controlWs.end();
    }
    router.remove(id);
    return reply.code(204).send();
  });
}

function stripTrailingSlash(s: string): string {
  return s.endsWith("/") ? s.slice(0, -1) : s;
}
