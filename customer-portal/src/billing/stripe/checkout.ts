import type { StripeClient, CheckoutSessionResult } from './types.js';

export interface CreateTopupCheckoutInput {
  customerId: string;
  amountUsdCents: number;
  successUrl: string;
  cancelUrl: string;
}

export interface TopupCheckoutConfig {
  priceMinCents: number;
  priceMaxCents: number;
}

export class InvalidTopupAmountError extends Error {
  readonly name = 'InvalidTopupAmountError';
  constructor(public readonly min: number, public readonly max: number) {
    super(`amount_usd_cents must be between ${min} and ${max}`);
  }
}

export async function createTopupCheckoutSession(
  stripe: StripeClient,
  config: TopupCheckoutConfig,
  input: CreateTopupCheckoutInput,
): Promise<CheckoutSessionResult> {
  if (
    input.amountUsdCents < config.priceMinCents ||
    input.amountUsdCents > config.priceMaxCents
  ) {
    throw new InvalidTopupAmountError(config.priceMinCents, config.priceMaxCents);
  }
  return stripe.createCheckoutSession({
    customerId: input.customerId,
    amountUsdCents: input.amountUsdCents,
    successUrl: input.successUrl,
    cancelUrl: input.cancelUrl,
  });
}
