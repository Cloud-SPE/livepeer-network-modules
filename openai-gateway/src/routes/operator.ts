import type { FastifyInstance, FastifyReply, FastifyRequest, preHandlerAsyncHookHandler } from 'fastify';
import type { AdminAuthResolver } from '@livepeer-network-modules/customer-portal/auth';
import { middleware } from '@livepeer-network-modules/customer-portal';
import { renderRouteHealthMetrics, summarizeRouteHealth } from '@livepeer-network-modules/gateway-route-health';

import type { RouteSelector } from '../service/routeSelector.js';
import type { RateCardSnapshot } from '../service/pricing/types.js';
import { loadRateCardSnapshot, replaceRateCardSnapshot, type Queryable } from '../repo/rateCard.js';

declare module 'fastify' {
  interface FastifyRequest {
    adminActor?: string;
  }
}

export interface RegisterOperatorRoutesDeps {
  authResolver: AdminAuthResolver;
  rateCardStore: Queryable & { connect?: () => Promise<{ query: (sql: string, args?: unknown[]) => Promise<{ rows: Record<string, unknown>[] }>; release: () => void }> };
  routeSelector: RouteSelector;
}

export function registerOperatorRoutes(app: FastifyInstance, deps: RegisterOperatorRoutesDeps): void {
  const preHandler = adminAuthPreHandler(deps.authResolver);

  app.get('/admin/openai/rate-card', { preHandler }, async (_req, reply) => {
    const snapshot = await loadRateCardSnapshot(deps.rateCardStore);
    await reply.code(200).send(snapshot);
  });

  app.put('/admin/openai/rate-card', { preHandler }, async (req, reply) => {
    try {
      const snapshot = req.body as RateCardSnapshot;
      await replaceRateCardSnapshot(deps.rateCardStore, snapshot);
      await reply.code(204).send();
    } catch (err) {
      const { status, envelope } = middleware.toHttpError(err);
      await reply.code(status).send(envelope);
    }
  });

  app.get('/admin/openai/resolver-candidates', { preHandler }, async (_req, reply) => {
    const candidates = await deps.routeSelector.inspect();
    const health = deps.routeSelector.inspectHealth();
    const metrics = deps.routeSelector.inspectMetrics();
    await reply.code(200).send({ candidates, health, summary: summarizeRouteHealth(health), metrics });
  });

  app.get('/admin/openai/route-health/metrics', { preHandler }, async (_req, reply) => {
    const health = deps.routeSelector.inspectHealth();
    const metrics = deps.routeSelector.inspectMetrics();
    await reply
      .code(200)
      .header('Content-Type', 'text/plain; version=0.0.4')
      .send(renderRouteHealthMetrics('openai', summarizeRouteHealth(health), metrics));
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
        error: { code: 'authentication_failed', message: 'admin token + actor required', type: 'AdminAuthError' },
      });
      return;
    }
    req.adminActor = result.actor;
  };
}
