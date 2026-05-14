/**
 * Per-session control-WS client.
 *
 * The mode spec requires the gateway to open the broker's control_url
 * immediately after session-open; otherwise the broker auto-closes
 * within `expires_at`. The control WS frame vocabulary is lifecycle-only
 * (session.started / session.usage.tick / session.balance.low /
 * session.balance.refilled / session.ended / session.error from broker;
 * session.end / session.topup from gateway).
 *
 * For v0 the gateway simply holds the WS open and logs received frames.
 * Customer-facing usage / balance reporting can be layered on top in a
 * follow-up plan.
 */

import WebSocket from "ws";

import type { FastifyBaseLogger } from "fastify";

export interface ControlWsHandle {
  /** Send a session.end frame and close the WS. */
  end(): void;
  /** Send a session.topup frame with a payment header. */
  topup(paymentHeader: string): void;
  /** True until the WS terminally closes. */
  isOpen(): boolean;
}

export interface OpenControlWsOpts {
  controlUrl: string;
  sessionId: string;
  logger: FastifyBaseLogger;
  onEnded?: () => void;
  onError?: (reason: string) => void;
}

export function openControlWs(opts: OpenControlWsOpts): ControlWsHandle {
  const ws = new WebSocket(opts.controlUrl);
  let open = true;

  ws.on("open", () => {
    opts.logger.info(
      { session_id: opts.sessionId, control_url: opts.controlUrl },
      "control-ws open",
    );
  });

  ws.on("message", (data) => {
    let env: { type?: string; body?: unknown };
    try {
      env = JSON.parse(data.toString());
    } catch {
      return;
    }
    if (typeof env.type !== "string") return;
    opts.logger.debug(
      { session_id: opts.sessionId, type: env.type, body: env.body },
      "control-ws frame",
    );
    switch (env.type) {
      case "session.ended":
        open = false;
        if (opts.onEnded) opts.onEnded();
        return;
      case "session.error":
        open = false;
        if (opts.onError) {
          const body = env.body as { code?: string } | undefined;
          opts.onError(body?.code ?? "unknown");
        }
        return;
      default:
        return;
    }
  });

  ws.on("close", () => {
    open = false;
    if (opts.onEnded) opts.onEnded();
  });

  ws.on("error", (err) => {
    open = false;
    opts.logger.warn(
      { session_id: opts.sessionId, err: err.message },
      "control-ws error",
    );
    if (opts.onError) opts.onError(err.message);
  });

  return {
    isOpen(): boolean {
      return open;
    },
    end(): void {
      try {
        ws.send(JSON.stringify({ type: "session.end" }));
      } catch {
        // ignore; close pump will handle teardown.
      }
      ws.close(1000);
    },
    topup(paymentHeader: string): void {
      try {
        ws.send(
          JSON.stringify({
            type: "session.topup",
            body: { payment_header: paymentHeader },
          }),
        );
      } catch {
        // ignore; balance.low → next topup attempt will retry.
      }
    },
  };
}
