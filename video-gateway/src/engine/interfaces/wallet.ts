import type { CostQuote, ReservationHandle, UsageReport } from "../types/index.js";

export interface Wallet {
  reserve(callerId: string, quote: CostQuote): Promise<ReservationHandle | null>;
  commit(handle: ReservationHandle, usage: UsageReport): Promise<void>;
  refund(handle: ReservationHandle): Promise<void>;
}
