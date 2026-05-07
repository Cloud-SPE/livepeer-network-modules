import type { FastifyReply, FastifyRequest, preHandlerAsyncHookHandler } from 'fastify';
import { RateLimitExceededError, type RateLimiter } from './types.js';

export function rateLimitPreHandler(limiter: RateLimiter): preHandlerAsyncHookHandler {
  return async (req: FastifyRequest, reply: FastifyReply): Promise<void> => {
    if (!req.caller) return;

    try {
      const result = await limiter.check(req.caller.id, req.caller.rateLimitTier);
      reply.header('x-ratelimit-limit-requests', String(result.headers.limitRequests));
      reply.header('x-ratelimit-remaining-requests', String(result.headers.remainingRequests));
      reply.header('x-ratelimit-reset-requests', String(result.headers.resetSeconds));
      req.rateLimit = {
        concurrencyKey: result.concurrencyKey,
        failedOpen: result.failedOpen,
      };
      reply.raw.on('close', () => {
        void limiter.release(result.concurrencyKey, result.failedOpen);
      });
    } catch (err) {
      if (err instanceof RateLimitExceededError) {
        reply.header('retry-after', String(err.retryAfterSeconds));
        reply.header('x-ratelimit-limit-requests', String(err.limit));
        reply.header('x-ratelimit-remaining-requests', '0');
        reply.header('x-ratelimit-reset-requests', String(err.retryAfterSeconds));
        await reply.code(429).send({
          error: {
            code: 'rate_limit_exceeded',
            type: err.name,
            message: err.message,
          },
        });
        return;
      }
      throw err;
    }
  };
}
