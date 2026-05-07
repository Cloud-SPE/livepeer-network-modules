import type { Db } from '../db/pool.js';
import { commit, commitQuota, refund, refundQuota, reserve, reserveQuota } from './reservations.js';
import type { CostQuote, ReservationHandle, UsageReport, Wallet } from './types.js';
import { UnknownCallerTierError } from './errors.js';

export interface PrepaidQuotaWalletDeps {
  db: Db;
}

interface PrepaidHandle {
  kind: 'prepaid';
  reservationId: string;
}

interface QuotaHandle {
  kind: 'quota';
  reservationId: string;
}

type Handle = PrepaidHandle | QuotaHandle;

export function createPrepaidQuotaWallet(deps: PrepaidQuotaWalletDeps): Wallet {
  return {
    async reserve(callerId: string, quote: CostQuote): Promise<ReservationHandle | null> {
      if (quote.callerTier === 'prepaid') {
        const result = await reserve(deps.db, {
          customerId: callerId,
          workId: quote.workId,
          estCostCents: quote.cents,
        });
        const handle: PrepaidHandle = { kind: 'prepaid', reservationId: result.reservationId };
        return handle;
      }
      if (quote.callerTier === 'free') {
        const result = await reserveQuota(deps.db, {
          customerId: callerId,
          workId: quote.workId,
          estTokens: BigInt(quote.estimatedTokens),
        });
        const handle: QuotaHandle = { kind: 'quota', reservationId: result.reservationId };
        return handle;
      }
      throw new UnknownCallerTierError(callerId, quote.callerTier);
    },

    async commit(handle: ReservationHandle, usage: UsageReport): Promise<void> {
      const h = handle as Handle;
      if (h.kind === 'prepaid') {
        await commit(deps.db, {
          reservationId: h.reservationId,
          actualCostCents: usage.cents,
          capability: usage.capability,
          model: usage.model,
          tier: 'prepaid',
        });
        return;
      }
      await commitQuota(deps.db, {
        reservationId: h.reservationId,
        actualTokens: BigInt(usage.actualTokens),
      });
    },

    async refund(handle: ReservationHandle): Promise<void> {
      const h = handle as Handle;
      if (h.kind === 'prepaid') {
        await refund(deps.db, h.reservationId);
        return;
      }
      await refundQuota(deps.db, h.reservationId);
    },
  };
}
