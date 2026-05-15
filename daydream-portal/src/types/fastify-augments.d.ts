// Module augmentations centralised so routes don't each redeclare them.

import "fastify";
import type { CustomerPortal } from "@livepeer-network-modules/customer-portal";

declare module "fastify" {
  interface FastifyRequest {
    customerSession?: Awaited<
      ReturnType<CustomerPortal["customerTokenService"]["authenticate"]>
    >;
    adminActor?: string;
  }
}
