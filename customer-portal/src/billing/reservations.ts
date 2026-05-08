import type { Db } from '../db/pool.js';
import * as customersRepo from '../repo/customers.js';
import * as reservationsRepo from '../repo/reservations.js';
import {
  BalanceInsufficientError,
  CustomerNotFoundError,
  QuotaExceededError,
  ReservationNotOpenError,
  TierMismatchError,
} from './errors.js';

export interface PrepaidReserveInput {
  customerId: string;
  workId: string;
  estCostCents: bigint;
}

export interface PrepaidReserveResult {
  reservationId: string;
  customerId: string;
  workId: string;
  amountUsdCents: bigint;
}

export async function reserve(db: Db, input: PrepaidReserveInput): Promise<PrepaidReserveResult> {
  return db.transaction(async (tx) => {
    const customer = await customersRepo.selectForUpdate(tx, input.customerId);
    if (!customer) throw new CustomerNotFoundError(input.customerId);
    if (customer.tier !== 'prepaid') {
      throw new TierMismatchError(input.customerId, 'prepaid', customer.tier);
    }

    const available = customer.balanceUsdCents - customer.reservedUsdCents;
    if (available < input.estCostCents) {
      throw new BalanceInsufficientError(available, input.estCostCents);
    }

    await customersRepo.updateBalanceFields(tx, input.customerId, {
      reservedUsdCents: customer.reservedUsdCents + input.estCostCents,
    });

    const reservation = await reservationsRepo.insertReservation(tx, {
      customerId: input.customerId,
      workId: input.workId,
      kind: 'prepaid',
      amountUsdCents: input.estCostCents,
      state: 'open',
    });

    return {
      reservationId: reservation.id,
      customerId: input.customerId,
      workId: input.workId,
      amountUsdCents: input.estCostCents,
    };
  });
}

export interface PrepaidCommitInput {
  reservationId: string;
  actualCostCents: bigint;
  capability?: string;
  model?: string;
  tier?: string;
}

export interface PrepaidCommitResult {
  reservationId: string;
  customerId: string;
  workId: string;
  actualUsdCents: bigint;
  refundedUsdCents: bigint;
  committedAt: Date;
}

export async function commit(db: Db, input: PrepaidCommitInput): Promise<PrepaidCommitResult> {
  return db.transaction(async (tx) => {
    const reservation = await reservationsRepo.findById(tx, input.reservationId);
    if (!reservation || reservation.state !== 'open') {
      throw new ReservationNotOpenError(input.reservationId);
    }
    if (reservation.kind !== 'prepaid') {
      throw new TierMismatchError(reservation.customerId, 'prepaid', 'free');
    }

    const reserved = reservation.amountUsdCents ?? 0n;
    const actual = input.actualCostCents > reserved ? reserved : input.actualCostCents;

    const customer = await customersRepo.selectForUpdate(tx, reservation.customerId);
    if (!customer) throw new CustomerNotFoundError(reservation.customerId);

    await customersRepo.updateBalanceFields(tx, reservation.customerId, {
      balanceUsdCents: customer.balanceUsdCents - actual,
      reservedUsdCents: customer.reservedUsdCents - reserved,
    });

    const committedAt = new Date();
    await reservationsRepo.updateState(tx, reservation.id, {
      state: 'committed',
      resolvedAt: committedAt,
      committedUsdCents: actual,
      refundedUsdCents: reserved - actual,
      capability: input.capability ?? null,
      model: input.model ?? null,
    });

    return {
      reservationId: reservation.id,
      customerId: reservation.customerId,
      workId: reservation.workId,
      actualUsdCents: actual,
      refundedUsdCents: reserved - actual,
      committedAt,
    };
  });
}

export interface PrepaidRefundResult {
  reservationId: string;
  customerId: string;
  workId: string;
  refundedUsdCents: bigint;
  refundedAt: Date;
}

export async function refund(db: Db, reservationId: string): Promise<PrepaidRefundResult> {
  return db.transaction(async (tx) => {
    const reservation = await reservationsRepo.findById(tx, reservationId);
    if (!reservation || reservation.state !== 'open') {
      throw new ReservationNotOpenError(reservationId);
    }
    if (reservation.kind !== 'prepaid') {
      throw new TierMismatchError(reservation.customerId, 'prepaid', 'free');
    }

    const reserved = reservation.amountUsdCents ?? 0n;

    const customer = await customersRepo.selectForUpdate(tx, reservation.customerId);
    if (!customer) throw new CustomerNotFoundError(reservation.customerId);

    await customersRepo.updateBalanceFields(tx, reservation.customerId, {
      reservedUsdCents: customer.reservedUsdCents - reserved,
    });

    const refundedAt = new Date();
    await reservationsRepo.updateState(tx, reservation.id, {
      state: 'refunded',
      resolvedAt: refundedAt,
      refundedUsdCents: reserved,
    });

    return {
      reservationId: reservation.id,
      customerId: reservation.customerId,
      workId: reservation.workId,
      refundedUsdCents: reserved,
      refundedAt,
    };
  });
}

export interface QuotaReserveInput {
  customerId: string;
  workId: string;
  estTokens: bigint;
}

export interface QuotaReserveResult {
  reservationId: string;
  customerId: string;
  workId: string;
  amountTokens: bigint;
}

export async function reserveQuota(db: Db, input: QuotaReserveInput): Promise<QuotaReserveResult> {
  return db.transaction(async (tx) => {
    const customer = await customersRepo.selectForUpdate(tx, input.customerId);
    if (!customer) throw new CustomerNotFoundError(input.customerId);
    if (customer.tier !== 'free') {
      throw new TierMismatchError(input.customerId, 'free', customer.tier);
    }

    const remaining = customer.quotaTokensRemaining ?? 0n;
    const reserved = customer.quotaReservedTokens;
    const available = remaining - reserved;
    if (available < input.estTokens) {
      throw new QuotaExceededError(available, input.estTokens);
    }

    await customersRepo.updateQuotaFields(tx, input.customerId, {
      quotaReservedTokens: reserved + input.estTokens,
    });

    const reservation = await reservationsRepo.insertReservation(tx, {
      customerId: input.customerId,
      workId: input.workId,
      kind: 'free',
      amountTokens: input.estTokens,
      state: 'open',
    });

    return {
      reservationId: reservation.id,
      customerId: input.customerId,
      workId: input.workId,
      amountTokens: input.estTokens,
    };
  });
}

export interface QuotaCommitInput {
  reservationId: string;
  actualTokens: bigint;
}

export interface QuotaCommitResult {
  reservationId: string;
  customerId: string;
  workId: string;
  actualTokens: bigint;
  refundedTokens: bigint;
  committedAt: Date;
}

export async function commitQuota(db: Db, input: QuotaCommitInput): Promise<QuotaCommitResult> {
  return db.transaction(async (tx) => {
    const reservation = await reservationsRepo.findById(tx, input.reservationId);
    if (!reservation || reservation.state !== 'open') {
      throw new ReservationNotOpenError(input.reservationId);
    }
    if (reservation.kind !== 'free') {
      throw new TierMismatchError(reservation.customerId, 'free', 'prepaid');
    }

    const reserved = reservation.amountTokens ?? 0n;
    const actual = input.actualTokens > reserved ? reserved : input.actualTokens;

    const customer = await customersRepo.selectForUpdate(tx, reservation.customerId);
    if (!customer) throw new CustomerNotFoundError(reservation.customerId);

    const remaining = customer.quotaTokensRemaining ?? 0n;
    await customersRepo.updateQuotaFields(tx, reservation.customerId, {
      quotaTokensRemaining: remaining - actual,
      quotaReservedTokens: customer.quotaReservedTokens - reserved,
    });

    const committedAt = new Date();
    await reservationsRepo.updateState(tx, reservation.id, {
      state: 'committed',
      resolvedAt: committedAt,
      committedTokens: actual,
      refundedTokens: reserved - actual,
    });

    return {
      reservationId: reservation.id,
      customerId: reservation.customerId,
      workId: reservation.workId,
      actualTokens: actual,
      refundedTokens: reserved - actual,
      committedAt,
    };
  });
}

export interface QuotaRefundResult {
  reservationId: string;
  customerId: string;
  workId: string;
  refundedTokens: bigint;
  refundedAt: Date;
}

export async function refundQuota(db: Db, reservationId: string): Promise<QuotaRefundResult> {
  return db.transaction(async (tx) => {
    const reservation = await reservationsRepo.findById(tx, reservationId);
    if (!reservation || reservation.state !== 'open') {
      throw new ReservationNotOpenError(reservationId);
    }
    if (reservation.kind !== 'free') {
      throw new TierMismatchError(reservation.customerId, 'free', 'prepaid');
    }

    const reserved = reservation.amountTokens ?? 0n;

    const customer = await customersRepo.selectForUpdate(tx, reservation.customerId);
    if (!customer) throw new CustomerNotFoundError(reservation.customerId);

    await customersRepo.updateQuotaFields(tx, reservation.customerId, {
      quotaReservedTokens: customer.quotaReservedTokens - reserved,
    });

    const refundedAt = new Date();
    await reservationsRepo.updateState(tx, reservation.id, {
      state: 'refunded',
      resolvedAt: refundedAt,
      refundedTokens: reserved,
    });

    return {
      reservationId: reservation.id,
      customerId: reservation.customerId,
      workId: reservation.workId,
      refundedTokens: reserved,
      refundedAt,
    };
  });
}
