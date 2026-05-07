export class CustomerNotFoundError extends Error {
  readonly name = 'CustomerNotFoundError';
  constructor(public readonly customerId: string) {
    super(`customer ${customerId} not found`);
  }
}

export class TierMismatchError extends Error {
  readonly name = 'TierMismatchError';
  constructor(
    public readonly customerId: string,
    public readonly expected: string,
    public readonly actual: string,
  ) {
    super(`customer ${customerId} tier mismatch: expected ${expected}, got ${actual}`);
  }
}

export class BalanceInsufficientError extends Error {
  readonly name = 'BalanceInsufficientError';
  constructor(public readonly available: bigint, public readonly required: bigint) {
    super(`balance insufficient: available ${available}, required ${required}`);
  }
}

export class QuotaExceededError extends Error {
  readonly name = 'QuotaExceededError';
  constructor(public readonly available: bigint, public readonly required: bigint) {
    super(`quota exceeded: available ${available}, required ${required}`);
  }
}

export class ReservationNotOpenError extends Error {
  readonly name = 'ReservationNotOpenError';
  constructor(public readonly reservationId: string) {
    super(`reservation ${reservationId} is not open`);
  }
}

export class UnknownCallerTierError extends Error {
  readonly name = 'UnknownCallerTierError';
  constructor(public readonly callerId: string, public readonly tier: string) {
    super(`unknown caller tier for ${callerId}: ${tier}`);
  }
}
