import type { FastifyReply, FastifyRequest, preHandlerAsyncHookHandler } from 'fastify';
import type { AuthResolver } from '../auth/types.js';
import { toHttpError } from './errors.js';

export function authPreHandler(authResolver: AuthResolver): preHandlerAsyncHookHandler {
  return async (req: FastifyRequest, reply: FastifyReply): Promise<void> => {
    const caller = await authResolver.resolve({
      headers: req.headers as Record<string, string | undefined>,
      ip: req.ip,
    });
    if (!caller) {
      const { status, envelope } = toHttpError(new Error('authentication required'));
      await reply.code(status === 500 ? 401 : status).send({
        error: {
          code: 'authentication_failed',
          message: 'authentication required',
          type: 'AuthError',
        },
      });
      void envelope;
      return;
    }
    req.caller = caller;
  };
}
