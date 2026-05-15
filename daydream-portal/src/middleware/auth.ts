// Shared Fastify pre-handlers. Customer auth defers entirely to
// customer-portal's CustomerTokenService — it parses the Bearer token,
// hashes against the configured pepper, and throws typed errors on
// failure. Admin auth defers to whichever AdminAuthResolver was
// returned by createCustomerPortal.

import type {
  FastifyReply,
  FastifyRequest,
  preHandlerAsyncHookHandler,
} from "fastify";
import type {
  CustomerPortal,
  auth,
} from "@livepeer-network-modules/customer-portal";

export function customerAuthPreHandler(
  service: CustomerPortal["customerTokenService"],
): preHandlerAsyncHookHandler {
  return async (req: FastifyRequest, reply: FastifyReply): Promise<void> => {
    try {
      req.customerSession = await service.authenticate(req.headers.authorization);
    } catch (err) {
      await reply.code(401).send({
        error: "authentication_failed",
        message: err instanceof Error ? err.message : String(err),
      });
    }
  };
}

export function adminAuthPreHandler(
  resolver: auth.AdminAuthResolver,
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
