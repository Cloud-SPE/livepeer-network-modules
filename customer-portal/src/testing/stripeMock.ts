import type {
  CheckoutSessionInput,
  CheckoutSessionResult,
  StripeClient,
  StripeEventMinimal,
} from '../billing/stripe/types.js';
import type { WebhookEventStore } from '../billing/stripe/webhook.js';

export interface MockStripeClientOptions {
  validSignature?: string;
  events?: Map<string, StripeEventMinimal>;
}

export function createMockStripeClient(options: MockStripeClientOptions = {}): StripeClient & {
  registerEvent(signature: string, event: StripeEventMinimal): void;
  checkoutCalls: CheckoutSessionInput[];
} {
  const events = options.events ?? new Map<string, StripeEventMinimal>();
  const checkoutCalls: CheckoutSessionInput[] = [];

  return {
    checkoutCalls,
    registerEvent(signature, event) {
      events.set(signature, event);
    },
    async createCheckoutSession(input: CheckoutSessionInput): Promise<CheckoutSessionResult> {
      checkoutCalls.push(input);
      return {
        sessionId: `cs_test_${checkoutCalls.length}`,
        url: `https://checkout.stripe.test/cs_test_${checkoutCalls.length}`,
      };
    },
    constructEvent(_rawBody, signature: string): StripeEventMinimal {
      const event = events.get(signature);
      if (!event) {
        throw new Error('signature_invalid');
      }
      return event;
    },
  };
}

export interface InMemoryWebhookEventStore extends WebhookEventStore {
  insertedEvents: Set<string>;
  topupCredits: Array<{ customerId: string; stripeSessionId: string; amountUsdCents: bigint }>;
  disputes: string[];
}

export function createInMemoryWebhookEventStore(): InMemoryWebhookEventStore {
  const insertedEvents = new Set<string>();
  const topupCredits: Array<{
    customerId: string;
    stripeSessionId: string;
    amountUsdCents: bigint;
  }> = [];
  const disputes: string[] = [];
  return {
    insertedEvents,
    topupCredits,
    disputes,
    async insertIfNew(eventId, _type, _payload) {
      if (insertedEvents.has(eventId)) return false;
      insertedEvents.add(eventId);
      return true;
    },
    async creditTopup(input) {
      topupCredits.push(input);
    },
    async markTopupDisputed(stripeSessionId) {
      disputes.push(stripeSessionId);
    },
  };
}
