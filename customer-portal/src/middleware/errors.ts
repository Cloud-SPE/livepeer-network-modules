import {
  AccountClosedError,
  AccountSuspendedError,
  InvalidApiKeyError,
  MalformedAuthorizationError,
} from '../auth/errors.js';
import {
  BalanceInsufficientError,
  CustomerNotFoundError,
  QuotaExceededError,
  ReservationNotOpenError,
  TierMismatchError,
  UnknownCallerTierError,
} from '../billing/errors.js';
import { InvalidTopupAmountError } from '../billing/stripe/checkout.js';
import { RateLimitExceededError } from './types.js';
import { ZodError } from 'zod';

export interface ErrorEnvelope {
  error: {
    code: string;
    message: string;
    type: string;
  };
}

export interface HttpError {
  status: number;
  envelope: ErrorEnvelope;
}

export function toHttpError(err: unknown): HttpError {
  if (err instanceof MalformedAuthorizationError || err instanceof InvalidApiKeyError) {
    return {
      status: 401,
      envelope: {
        error: { code: 'authentication_failed', message: err.message, type: err.name },
      },
    };
  }
  if (err instanceof AccountSuspendedError || err instanceof AccountClosedError) {
    return {
      status: 403,
      envelope: { error: { code: 'account_inactive', message: err.message, type: err.name } },
    };
  }
  if (err instanceof RateLimitExceededError) {
    return {
      status: 429,
      envelope: { error: { code: 'rate_limit_exceeded', message: err.message, type: err.name } },
    };
  }
  if (
    err instanceof BalanceInsufficientError ||
    err instanceof QuotaExceededError
  ) {
    return {
      status: 402,
      envelope: { error: { code: 'payment_required', message: err.message, type: err.name } },
    };
  }
  if (err instanceof CustomerNotFoundError) {
    return {
      status: 404,
      envelope: { error: { code: 'not_found', message: err.message, type: err.name } },
    };
  }
  if (
    err instanceof TierMismatchError ||
    err instanceof UnknownCallerTierError ||
    err instanceof ReservationNotOpenError
  ) {
    return {
      status: 409,
      envelope: { error: { code: 'conflict', message: err.message, type: err.name } },
    };
  }
  if (err instanceof InvalidTopupAmountError) {
    return {
      status: 400,
      envelope: { error: { code: 'invalid_request_error', message: err.message, type: err.name } },
    };
  }
  if (err instanceof ZodError) {
    return {
      status: 400,
      envelope: {
        error: {
          code: 'invalid_request_error',
          message: err.message,
          type: 'ZodError',
        },
      },
    };
  }
  if (err instanceof Error) {
    return {
      status: 500,
      envelope: {
        error: { code: 'internal_error', message: err.message, type: err.name || 'Error' },
      },
    };
  }
  return {
    status: 500,
    envelope: { error: { code: 'internal_error', message: 'unknown error', type: 'Error' } },
  };
}
