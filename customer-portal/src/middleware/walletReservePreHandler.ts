import type { FastifyReply, FastifyRequest, preHandlerAsyncHookHandler } from 'fastify';
import type { CostQuote, ReservationHandle, UsageReport, Wallet } from '../billing/types.js';
import { toHttpError } from './errors.js';

export interface WalletReserveDeps {
  wallet: Wallet;
  quote: (req: FastifyRequest) => Promise<CostQuote> | CostQuote;
}

export function walletReservePreHandler(deps: WalletReserveDeps): preHandlerAsyncHookHandler {
  return async (req: FastifyRequest, reply: FastifyReply): Promise<void> => {
    if (!req.caller) {
      await reply.code(401).send({
        error: {
          code: 'authentication_failed',
          message: 'authentication required before reservation',
          type: 'AuthError',
        },
      });
      return;
    }
    try {
      const quote = await deps.quote(req);
      const handle = await deps.wallet.reserve(req.caller.id, quote);
      if (handle !== null) {
        req.walletReservation = { handle, workId: quote.workId };
      }
    } catch (err) {
      const { status, envelope } = toHttpError(err);
      await reply.code(status).send(envelope);
    }
  };
}

export async function commitOrRefund(
  wallet: Wallet,
  handle: ReservationHandle | undefined,
  result: { ok: true; usage: UsageReport } | { ok: false },
): Promise<void> {
  if (handle === undefined || handle === null) return;
  if (result.ok) {
    await wallet.commit(handle, result.usage);
    return;
  }
  try {
    await wallet.refund(handle);
  } catch {
    // refund is best-effort
  }
}
