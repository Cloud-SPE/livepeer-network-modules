import type { FastifyInstance, FastifyReply, FastifyRequest, preHandlerAsyncHookHandler } from "fastify";
import type { CustomerPortal } from "@livepeer-rewrite/customer-portal";

import { defaultPricingConfig } from "../engine/config/pricing.js";

declare module "fastify" {
  interface FastifyRequest {
    customerSession?: Awaited<ReturnType<CustomerPortal["customerTokenService"]["authenticate"]>>;
  }
}

export interface RegisterVideoCustomerPortalRoutesDeps {
  portal: CustomerPortal;
}

export function registerVideoCustomerPortalRoutes(
  app: FastifyInstance,
  deps: RegisterVideoCustomerPortalRoutesDeps,
): void {
  const requireCustomer = customerAuthPreHandler(deps.portal.customerTokenService);

  app.get("/portal/pricing", { preHandler: requireCustomer }, async (_req, reply) => {
    const pricing = defaultPricingConfig();
    await reply.code(200).send({
      live: {
        billing_unit: "stream_seconds",
        cents_per_second: pricing.liveCentsPerSecond,
        cents_per_minute: Number((pricing.liveCentsPerSecond * 60).toFixed(6)),
      },
      vod: {
        billing_unit: "rendition_seconds",
        overhead_cents: pricing.overheadCents,
        cents_per_second: pricing.vodCentsPerSecond,
      },
    });
  });
}

function customerAuthPreHandler(
  service: CustomerPortal["customerTokenService"],
): preHandlerAsyncHookHandler {
  return async (req: FastifyRequest, reply: FastifyReply): Promise<void> => {
    try {
      req.customerSession = await service.authenticate(req.headers.authorization);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      await reply.code(401).send({ error: "authentication_failed", message });
    }
  };
}
