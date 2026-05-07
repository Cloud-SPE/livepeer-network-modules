export interface CheckoutSessionInput {
  customerId: string;
  amountUsdCents: number;
  successUrl: string;
  cancelUrl: string;
}

export interface CheckoutSessionResult {
  sessionId: string;
  url: string;
}

export interface StripeEventMinimal {
  id: string;
  type: string;
  data: { object: Record<string, unknown> };
}

export interface StripeClient {
  createCheckoutSession(input: CheckoutSessionInput): Promise<CheckoutSessionResult>;
  constructEvent(rawBody: Buffer | string, signature: string): StripeEventMinimal;
}
