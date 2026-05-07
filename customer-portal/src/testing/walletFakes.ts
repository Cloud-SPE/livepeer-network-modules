import type { CostQuote, ReservationHandle, UsageReport, Wallet } from '../billing/types.js';
import { ReservationNotOpenError } from '../billing/errors.js';

interface FakeReservation {
  id: string;
  workId: string;
  callerId: string;
  cents: bigint;
  state: 'open' | 'committed' | 'refunded';
}

export interface InMemoryWalletState {
  reservationsById: Map<string, FakeReservation>;
  reservationsByWorkId: Map<string, FakeReservation>;
}

export interface InMemoryWalletDeps {
  initialBalanceCents?: bigint;
}

export function createInMemoryWallet(deps?: InMemoryWalletDeps): Wallet & {
  state: InMemoryWalletState;
  balance(callerId: string): bigint;
} {
  const balances = new Map<string, bigint>();
  const state: InMemoryWalletState = {
    reservationsById: new Map(),
    reservationsByWorkId: new Map(),
  };
  const initial = deps?.initialBalanceCents ?? 100_000n;

  function getBalance(callerId: string): bigint {
    if (!balances.has(callerId)) balances.set(callerId, initial);
    return balances.get(callerId)!;
  }

  return {
    state,
    balance(callerId: string): bigint {
      return getBalance(callerId);
    },
    async reserve(callerId: string, quote: CostQuote): Promise<ReservationHandle | null> {
      const balance = getBalance(callerId);
      if (balance < quote.cents) {
        throw new Error(`balance insufficient: ${balance} < ${quote.cents}`);
      }
      if (state.reservationsByWorkId.has(quote.workId)) {
        throw new Error(`workId ${quote.workId} already has a reservation`);
      }
      const id = `res_${state.reservationsById.size + 1}`;
      const r: FakeReservation = {
        id,
        workId: quote.workId,
        callerId,
        cents: quote.cents,
        state: 'open',
      };
      state.reservationsById.set(id, r);
      state.reservationsByWorkId.set(quote.workId, r);
      balances.set(callerId, balance - quote.cents);
      return { reservationId: id };
    },
    async commit(handle: ReservationHandle, usage: UsageReport): Promise<void> {
      const h = handle as { reservationId: string };
      const r = state.reservationsById.get(h.reservationId);
      if (!r || r.state !== 'open') throw new ReservationNotOpenError(h.reservationId);
      const refundCents = r.cents > usage.cents ? r.cents - usage.cents : 0n;
      const balance = getBalance(r.callerId);
      balances.set(r.callerId, balance + refundCents);
      r.state = 'committed';
    },
    async refund(handle: ReservationHandle): Promise<void> {
      const h = handle as { reservationId: string };
      const r = state.reservationsById.get(h.reservationId);
      if (!r || r.state !== 'open') throw new ReservationNotOpenError(h.reservationId);
      const balance = getBalance(r.callerId);
      balances.set(r.callerId, balance + r.cents);
      r.state = 'refunded';
    },
  };
}
