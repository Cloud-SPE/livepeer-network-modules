import { eq } from 'drizzle-orm';
import type { Db } from '../db/pool.js';
import * as customersRepo from '../repo/customers.js';
import * as topupsRepo from '../repo/topups.js';
import { customers, topups } from '../db/schema.js';
import { CustomerNotFoundError } from './errors.js';

export interface CreditTopupInput {
  customerId: string;
  stripeSessionId: string;
  amountUsdCents: bigint;
}

export interface CreditTopupResult {
  customerId: string;
  stripeSessionId: string;
  creditedUsdCents: bigint;
  newBalanceUsdCents: bigint;
  upgradedFromFree: boolean;
}

export async function creditTopup(
  db: Db,
  input: CreditTopupInput,
): Promise<CreditTopupResult> {
  return db.transaction(async (tx) => {
    const customer = await customersRepo.selectForUpdate(tx, input.customerId);
    if (!customer) throw new CustomerNotFoundError(input.customerId);

    await topupsRepo.insertTopup(tx, {
      customerId: input.customerId,
      stripeSessionId: input.stripeSessionId,
      amountUsdCents: input.amountUsdCents,
      status: 'succeeded',
    });

    const newBalance = customer.balanceUsdCents + input.amountUsdCents;
    const upgradedFromFree = customer.tier === 'free';

    await tx
      .update(customers)
      .set({
        balanceUsdCents: newBalance,
        ...(upgradedFromFree
          ? { tier: 'prepaid' as const, rateLimitTier: 'prepaid-default' }
          : {}),
      })
      .where(eq(customers.id, input.customerId));

    return {
      customerId: input.customerId,
      stripeSessionId: input.stripeSessionId,
      creditedUsdCents: input.amountUsdCents,
      newBalanceUsdCents: newBalance,
      upgradedFromFree,
    };
  });
}

export async function markTopupDisputed(
  db: Db,
  stripeSessionId: string,
  disputedAt: Date = new Date(),
): Promise<boolean> {
  const result = await db
    .update(topups)
    .set({ disputedAt })
    .where(eq(topups.stripeSessionId, stripeSessionId))
    .returning({ id: topups.id });
  return result.length > 0;
}

export interface ReverseTopupInput {
  stripeSessionId: string;
  reason: string;
}

export interface ReverseTopupResult {
  stripeSessionId: string;
  customerId: string;
  amountReversedCents: string;
  newBalanceUsdCents: string;
  alreadyRefunded: boolean;
}

export async function reverseTopup(db: Db, input: ReverseTopupInput): Promise<ReverseTopupResult> {
  if (input.reason.trim().length === 0) {
    throw new Error('reverseTopup: reason must not be empty');
  }
  return db.transaction(async (tx) => {
    const topup = await topupsRepo.findBySession(tx, input.stripeSessionId);
    if (!topup) {
      throw new Error(`reverseTopup: no topup for session ${input.stripeSessionId}`);
    }
    if (topup.refundedAt !== null) {
      const customer = await customersRepo.findById(tx, topup.customerId);
      return {
        stripeSessionId: input.stripeSessionId,
        customerId: topup.customerId,
        amountReversedCents: '0',
        newBalanceUsdCents: (customer?.balanceUsdCents ?? 0n).toString(),
        alreadyRefunded: true,
      };
    }

    const customer = await customersRepo.selectForUpdate(tx, topup.customerId);
    if (!customer) throw new CustomerNotFoundError(topup.customerId);

    const newBalance =
      customer.balanceUsdCents > topup.amountUsdCents
        ? customer.balanceUsdCents - topup.amountUsdCents
        : 0n;
    const actuallyReversed =
      customer.balanceUsdCents > topup.amountUsdCents
        ? topup.amountUsdCents
        : customer.balanceUsdCents;

    await tx
      .update(customers)
      .set({ balanceUsdCents: newBalance })
      .where(eq(customers.id, topup.customerId));
    await tx
      .update(topups)
      .set({ refundedAt: new Date(), status: 'refunded' })
      .where(eq(topups.id, topup.id));

    return {
      stripeSessionId: input.stripeSessionId,
      customerId: topup.customerId,
      amountReversedCents: actuallyReversed.toString(),
      newBalanceUsdCents: newBalance.toString(),
      alreadyRefunded: false,
    };
  });
}
