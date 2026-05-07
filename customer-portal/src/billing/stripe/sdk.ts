import Stripe from 'stripe';
import type {
  CheckoutSessionInput,
  CheckoutSessionResult,
  StripeClient,
  StripeEventMinimal,
} from './types.js';

export interface SdkStripeConfig {
  secretKey: string;
  webhookSecret: string;
  apiVersion?: string;
}

export function createSdkStripeClient(config: SdkStripeConfig): StripeClient {
  const stripe = new Stripe(config.secretKey, {
    apiVersion: (config.apiVersion ?? '2024-10-28.acacia') as Stripe.LatestApiVersion,
    typescript: true,
  });

  return {
    async createCheckoutSession(input: CheckoutSessionInput): Promise<CheckoutSessionResult> {
      const session = await stripe.checkout.sessions.create({
        mode: 'payment',
        payment_method_types: ['card'],
        client_reference_id: input.customerId,
        metadata: { customer_id: input.customerId },
        line_items: [
          {
            price_data: {
              currency: 'usd',
              unit_amount: input.amountUsdCents,
              product_data: { name: 'API Credits' },
            },
            quantity: 1,
          },
        ],
        success_url: input.successUrl,
        cancel_url: input.cancelUrl,
      });
      if (!session.url) throw new Error('stripe checkout session has no url');
      return { sessionId: session.id, url: session.url };
    },

    constructEvent(rawBody, signature): StripeEventMinimal {
      const event = stripe.webhooks.constructEvent(rawBody, signature, config.webhookSecret);
      return {
        id: event.id,
        type: event.type,
        data: { object: event.data.object as unknown as Record<string, unknown> },
      };
    },
  };
}
