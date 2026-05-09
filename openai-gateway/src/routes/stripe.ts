import type { FastifyInstance, FastifyPluginAsync } from 'fastify';
import { z } from 'zod';
import type { CustomerPortal } from '@livepeer-rewrite/customer-portal';
import { billing, middleware } from '@livepeer-rewrite/customer-portal';

import type { Config } from '../config.js';
import type { Db } from '@livepeer-rewrite/customer-portal/db';

const CheckoutSchema = z.object({
  amount_usd_cents: z.number().int().positive(),
});

export interface RegisterStripeRoutesDeps {
  cfg: Config;
  db: Db;
  portal: CustomerPortal;
}

export function registerStripeRoutes(app: FastifyInstance, deps: RegisterStripeRoutesDeps): void {
  const stripeCfg = deps.cfg.stripe;
  const stripeClient = deps.portal.stripe;
  if (!stripeCfg || !stripeClient) return;

  app.post('/portal/topups/checkout', async (req, reply) => {
    try {
      const customerSession = await deps.portal.customerTokenService.authenticate(req.headers.authorization);
      const parsed = CheckoutSchema.parse(req.body);
      const baseUrl = deps.cfg.publicBaseUrl ?? inferBaseUrl(req);
      const checkout = await billing.stripe.createTopupCheckoutSession(
        stripeClient,
        {
          priceMinCents: stripeCfg.topupMinCents,
          priceMaxCents: stripeCfg.topupMaxCents,
        },
        {
          customerId: customerSession.customer.id,
          amountUsdCents: parsed.amount_usd_cents,
          successUrl: `${baseUrl}/portal/#billing?checkout=success`,
          cancelUrl: `${baseUrl}/portal/#billing?checkout=cancel`,
        },
      );
      await reply.code(200).send({
        session_id: checkout.sessionId,
        url: checkout.url,
      });
    } catch (err) {
      const { status, envelope } = middleware.toHttpError(err);
      await reply.code(status).send(envelope);
    }
  });

  void app.register(stripeWebhookPlugin(deps, stripeClient), { prefix: '/portal/stripe' });
}

function stripeWebhookPlugin(
  deps: RegisterStripeRoutesDeps,
  stripeClient: NonNullable<CustomerPortal['stripe']>,
): FastifyPluginAsync {
  return async function stripeWebhookScope(scope) {
    scope.addContentTypeParser(
      'application/json',
      { parseAs: 'string' },
      (_req, body, done) => done(null, body),
    );

    scope.post('/webhook', async (req, reply) => {
      const rawBody =
        typeof req.body === 'string' || Buffer.isBuffer(req.body)
          ? req.body
          : JSON.stringify(req.body ?? {});
      const result = await billing.stripe.handleStripeWebhook(
        {
          store: deps.portal.webhookEventStore,
          stripe: stripeClient,
        },
        {
          rawBody,
          signature: req.headers['stripe-signature'] as string | undefined,
        },
      );
      const status =
        result.outcome === 'signature_invalid'
          ? 400
          : result.outcome === 'handler_error'
            ? 500
            : 200;
      await reply.code(status).send(result);
    });
  };
}

function inferBaseUrl(req: { protocol: string; headers: Record<string, unknown> }): string {
  const host = String(req.headers['x-forwarded-host'] ?? req.headers['host'] ?? 'localhost:3000');
  const proto = String(req.headers['x-forwarded-proto'] ?? req.protocol ?? 'http');
  return `${proto}://${host}`;
}
