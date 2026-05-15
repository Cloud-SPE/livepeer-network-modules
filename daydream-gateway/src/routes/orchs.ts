/**
 * GET /v1/orchs — list orchestrators advertising the `daydream-scope`
 * capability. Useful for consumers that want to display "N orchs
 * available" or pick one explicitly; the gateway itself auto-selects
 * on session-open, so this is informational only.
 */

import type { FastifyInstance } from "fastify";
import { renderRouteHealthMetrics, summarizeRouteHealth } from "@livepeer-network-modules/gateway-route-health";

import type { OrchSelector } from "../orchSelector.js";

export function registerOrchRoutes(
  app: FastifyInstance,
  selector: OrchSelector,
): void {
  app.addHook("onClose", async () => {
    await selector.close?.();
  });
  app.get("/v1/orchs", async () => {
    const candidates = await selector.list();
    const health = selector.inspectHealth();
    const healthByKey = new Map(
      health.map((entry) => [entry.key, entry]),
    );
    return {
      capability: "daydream-scope",
      summary: summarizeRouteHealth(health),
      metrics: selector.inspectMetrics(),
      orchs: candidates.map((c) => ({
        eth_address: c.ethAddress,
        broker_url: c.brokerUrl,
        offering: c.offering,
        work_unit: c.workUnit,
        price_per_work_unit_wei: c.pricePerWorkUnitWei,
        route_health: healthByKey.get(
          [c.brokerUrl, c.ethAddress, c.capability, c.offering].join("|"),
        ) ?? null,
      })),
    };
  });

  app.get("/v1/orchs/metrics", async (_req, reply) => {
    const health = selector.inspectHealth();
    const metrics = selector.inspectMetrics();
    await reply
      .code(200)
      .header("Content-Type", "text/plain; version=0.0.4")
      .send(renderRouteHealthMetrics("daydream", summarizeRouteHealth(health), metrics));
  });
}
