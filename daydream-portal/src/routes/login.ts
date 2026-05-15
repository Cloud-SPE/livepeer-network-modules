// Key-for-token exchange. The two-track auth model means the user has
// a single durable API key (used by external clients hitting
// daydream-gateway) and the portal SPA gets a short-lived UI token
// minted from that key at sign-in time.
//
// customer-portal ships /portal/login but it validates an existing
// UI auth token, not the API key. This endpoint bridges that gap: it
// authenticates the API key via authService, then issues a UI token
// via customerTokenService.

import type { FastifyInstance } from "fastify";
import { z } from "zod";
import type { CustomerPortal } from "@livepeer-network-modules/customer-portal";

const LoginByKeySchema = z.object({
  api_key: z.string().min(1),
  // Free-text actor label — surfaced in audit events and the UI token
  // record. The portal SPA defaults to the user's email local-part.
  actor: z.string().trim().min(1).max(120),
});

export interface RegisterLoginRoutesDeps {
  portal: CustomerPortal;
}

export function registerLoginRoutes(
  app: FastifyInstance,
  deps: RegisterLoginRoutesDeps,
): void {
  app.post("/portal/login-by-key", async (req, reply) => {
    const parsed = LoginByKeySchema.safeParse(req.body);
    if (!parsed.success) {
      await reply
        .code(400)
        .send({ error: "invalid_request", details: parsed.error.flatten() });
      return;
    }
    try {
      const caller = await deps.portal.authService.authenticate(
        `Bearer ${parsed.data.api_key}`,
      );
      const issued = await deps.portal.customerTokenService.issue({
        customerId: caller.customer.id,
        label: `portal-ui:${parsed.data.actor}`,
      });
      await reply.code(200).send({
        auth_token: issued.plaintext,
        auth_token_id: issued.tokenId,
        customer: {
          id: caller.customer.id,
          email: caller.customer.email,
        },
      });
    } catch (err) {
      await reply.code(401).send({
        error: "authentication_failed",
        message: err instanceof Error ? err.message : String(err),
      });
    }
  });
}
