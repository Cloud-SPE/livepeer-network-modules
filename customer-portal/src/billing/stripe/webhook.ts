import type { Db } from '../../db/pool.js';
import * as stripeWebhookEventsRepo from '../../repo/stripeWebhookEvents.js';
import { creditTopup, markTopupDisputed } from '../topups.js';
import type { StripeClient, StripeEventMinimal } from './types.js';

export type WebhookOutcome =
  | 'processed'
  | 'duplicate'
  | 'signature_invalid'
  | 'handler_error'
  | 'unsupported';

export interface WebhookEventStore {
  insertIfNew(eventId: string, type: string, payload: string): Promise<boolean>;
  creditTopup(input: {
    customerId: string;
    stripeSessionId: string;
    amountUsdCents: bigint;
  }): Promise<void>;
  markTopupDisputed(stripeSessionId: string): Promise<void>;
}

export function createDbWebhookEventStore(db: Db): WebhookEventStore {
  return {
    async insertIfNew(eventId, type, payload) {
      return stripeWebhookEventsRepo.insertIfNew(db, eventId, type, payload);
    },
    async creditTopup(input) {
      await creditTopup(db, input);
    },
    async markTopupDisputed(stripeSessionId) {
      await markTopupDisputed(db, stripeSessionId);
    },
  };
}

export interface WebhookHandlerDeps {
  store: WebhookEventStore;
  stripe: StripeClient;
}

export interface WebhookInput {
  rawBody: string | Buffer;
  signature: string | undefined;
}

export interface WebhookResult {
  outcome: WebhookOutcome;
  eventType?: string;
  message?: string;
}

export async function handleStripeWebhook(
  deps: WebhookHandlerDeps,
  input: WebhookInput,
): Promise<WebhookResult> {
  if (typeof input.signature !== 'string') {
    return {
      outcome: 'signature_invalid',
      message: 'stripe-signature header missing',
    };
  }

  let event: StripeEventMinimal;
  try {
    event = deps.stripe.constructEvent(input.rawBody, input.signature);
  } catch {
    return {
      outcome: 'signature_invalid',
      message: 'stripe signature verification failed',
    };
  }

  const payloadJson =
    typeof input.rawBody === 'string' ? input.rawBody : input.rawBody.toString('utf8');
  const isNew = await deps.store.insertIfNew(event.id, event.type, payloadJson);
  if (!isNew) {
    return { outcome: 'duplicate', eventType: event.type };
  }

  try {
    const handled = await dispatchEvent(deps.store, event);
    return {
      outcome: handled ? 'processed' : 'unsupported',
      eventType: event.type,
    };
  } catch (err) {
    return {
      outcome: 'handler_error',
      eventType: event.type,
      message: err instanceof Error ? err.message : 'unknown',
    };
  }
}

async function dispatchEvent(
  store: WebhookEventStore,
  event: StripeEventMinimal,
): Promise<boolean> {
  if (event.type === 'checkout.session.completed') {
    const obj = event.data.object as {
      client_reference_id?: string;
      metadata?: { customer_id?: string };
      amount_total?: number;
      id?: string;
    };
    const customerId = obj.client_reference_id ?? obj.metadata?.customer_id;
    const sessionId = obj.id;
    const amount = obj.amount_total;
    if (!customerId || !sessionId || typeof amount !== 'number') {
      throw new Error('checkout.session.completed missing customer_id / session_id / amount');
    }
    await store.creditTopup({
      customerId,
      stripeSessionId: sessionId,
      amountUsdCents: BigInt(amount),
    });
    return true;
  }

  if (event.type === 'charge.dispute.created') {
    const obj = event.data.object as {
      metadata?: { stripe_session_id?: string };
    };
    const sessionId = obj.metadata?.stripe_session_id;
    if (sessionId) {
      await store.markTopupDisputed(sessionId);
      return true;
    }
    return false;
  }

  return false;
}
