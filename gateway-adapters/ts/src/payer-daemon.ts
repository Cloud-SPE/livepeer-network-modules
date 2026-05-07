// Thin TypeScript wrapper around the payer-daemon's
// `PayerDaemon.GetSessionDebits` RPC. Used by the long-lived mode
// adapters (`ws-realtime`, `session-control-plus-media`) to surface a
// final `Livepeer-Work-Units` count to the gateway caller on session
// close.
//
// The daemon may return UNIMPLEMENTED when its long-lived debit
// tracking is not wired (the actual debit-pushdown from the broker
// ticker lands with plan 0015). The adapters treat UNIMPLEMENTED as
// "0 work units, best effort" so they remain usable today.
//
// `@grpc/grpc-js` and `@grpc/proto-loader` are optional peerDependencies;
// gateways that already load the payer-daemon client (e.g. the reference
// `openai-gateway/`) inject the resolved client via `init({ client })`.

import type * as grpc from "@grpc/grpc-js";

export interface SessionDebits {
  totalWorkUnits: number;
  debitCount: number;
  closed: boolean;
}

export interface SessionDebitsClient {
  /**
   * Returns the per-session debit total for `(sender, workId)`. Returns
   * a zero-valued envelope (and a `false` `available` flag on the
   * caller's side) when the daemon answers UNIMPLEMENTED.
   */
  getSessionDebits(input: {
    sender: Uint8Array;
    workId: string;
  }): Promise<SessionDebits>;
}

/**
 * gRPC method shape `PayerDaemon.GetSessionDebits` exposes when the
 * proto definition is loaded with @grpc/proto-loader's keepCase=false
 * defaults.
 */
interface PayerDaemonRawClient extends grpc.Client {
  getSessionDebits(
    req: { sender: Uint8Array | Buffer; workId: string },
    cb: (
      err: grpc.ServiceError | null,
      resp: { totalWorkUnits: string | number; debitCount: string | number; closed: boolean },
    ) => void,
  ): void;
}

const GRPC_STATUS_UNIMPLEMENTED = 12;

/**
 * Wraps a raw gRPC payer-daemon client into a `SessionDebitsClient`.
 * The wrapper translates UNIMPLEMENTED into a zero-valued result; any
 * other gRPC error propagates to the caller.
 */
export function fromGrpcClient(client: PayerDaemonRawClient): SessionDebitsClient {
  return {
    getSessionDebits(input): Promise<SessionDebits> {
      return new Promise<SessionDebits>((resolve, reject) => {
        client.getSessionDebits(
          { sender: Buffer.from(input.sender), workId: input.workId },
          (err, resp) => {
            if (err) {
              if (err.code === GRPC_STATUS_UNIMPLEMENTED) {
                resolve({ totalWorkUnits: 0, debitCount: 0, closed: false });
                return;
              }
              reject(err);
              return;
            }
            resolve({
              totalWorkUnits: toNumber(resp.totalWorkUnits),
              debitCount: toNumber(resp.debitCount),
              closed: !!resp.closed,
            });
          },
        );
      });
    },
  };
}

function toNumber(v: string | number): number {
  if (typeof v === "number") return v;
  const n = parseInt(v, 10);
  return Number.isNaN(n) ? 0 : n;
}
