/**
 * GET /v1/orchs — list orchestrators advertising the `daydream-scope`
 * capability. Useful for consumers that want to display "N orchs
 * available" or pick one explicitly; the gateway itself auto-selects
 * on session-open, so this is informational only.
 */

import type { FastifyInstance } from "fastify";

import type { OrchSelector } from "../orchSelector.js";

export function registerOrchRoutes(
  app: FastifyInstance,
  selector: OrchSelector,
): void {
  app.get("/v1/orchs", async () => {
    const candidates = await selector.list();
    return {
      capability: "daydream-scope",
      orchs: candidates.map((c) => ({
        eth_address: c.ethAddress,
        broker_url: c.brokerUrl,
        offering: c.offering,
        work_unit: c.workUnit,
        price_per_work_unit_wei: c.pricePerWorkUnitWei,
      })),
    };
  });
}
