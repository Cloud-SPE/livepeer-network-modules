import type { FastifyInstance } from "fastify";
import { middleware } from "@livepeer-rewrite/customer-portal";
import type { AuthResolver } from "@livepeer-rewrite/customer-portal/auth";

import type { RouteSelector } from "../service/routeSelector.js";
import { buildModelCatalog } from "../service/catalog.js";

export function registerModelsRoute(
  app: FastifyInstance,
  authResolver: AuthResolver,
  routeSelector: RouteSelector,
): void {
  const preHandler = middleware.authPreHandler(authResolver);
  app.get("/v1/models", { preHandler }, async (_req, reply) => {
    const models = buildModelCatalog(await routeSelector.inspect());
    await reply.code(200).send({
      object: "list",
      data: models.map((model) => ({
        id: model.id,
        object: "model",
        owned_by: "livepeer",
        capability: model.capability,
        offering: model.offering,
        broker_url: model.brokerUrl,
        eth_address: model.ethAddress,
        price_per_work_unit_wei: model.pricePerWorkUnitWei,
        work_unit: model.workUnit,
        extra: model.extra,
        constraints: model.constraints,
      })),
    });
  });
}
