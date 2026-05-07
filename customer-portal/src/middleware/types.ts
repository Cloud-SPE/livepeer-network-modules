import type { Caller } from '../auth/types.js';

declare module 'fastify' {
  interface FastifyRequest {
    caller?: Caller;
    walletReservation?: {
      handle: unknown;
      workId: string;
    };
    rateLimit?: {
      concurrencyKey: string;
      failedOpen: boolean;
    };
  }
}

export interface RateLimitHeaders {
  limitRequests: number;
  remainingRequests: number;
  resetSeconds: number;
}

export interface RateLimitCheckResult {
  headers: RateLimitHeaders;
  concurrencyKey: string;
  failedOpen: boolean;
}

export interface RateLimiter {
  check(callerId: string, policyName: string): Promise<RateLimitCheckResult>;
  release(concurrencyKey: string, failedOpen: boolean): Promise<void>;
}

export class RateLimitExceededError extends Error {
  readonly name = 'RateLimitExceededError';
  constructor(
    public readonly limit: number,
    public readonly retryAfterSeconds: number,
  ) {
    super(`rate limit exceeded; retry after ${retryAfterSeconds}s`);
  }
}
