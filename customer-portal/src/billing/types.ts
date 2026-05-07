export interface CostQuote {
  workId: string;
  cents: bigint;
  estimatedTokens: number;
  model: string;
  capability: string;
  callerTier: string;
}

export interface UsageReport {
  cents: bigint;
  actualTokens: number;
  model: string;
  capability: string;
}

export type ReservationHandle = unknown;

export interface Wallet {
  reserve(callerId: string, quote: CostQuote): Promise<ReservationHandle | null>;
  commit(handle: ReservationHandle, usage: UsageReport): Promise<void>;
  refund(handle: ReservationHandle): Promise<void>;
}

export interface RateCardResolver {
  resolve(input: { capability: string; offering: string; callerTier?: string }): Promise<{
    usdPerUnit: bigint;
    unit: string;
  } | null>;
}
