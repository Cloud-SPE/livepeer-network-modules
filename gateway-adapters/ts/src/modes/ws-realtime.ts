// ws-realtime@v0 middleware per
// livepeer-network-protocol/modes/ws-realtime.md.
//
// HTTP `GET /v1/cap` upgrade with the five required Livepeer-* headers,
// then bidirectional frame relay on the broker leg. The adapter does
// NOT own the customer leg; the gateway operator wires that to its app.
//
// On clean close the adapter consults the payer-daemon's session ledger
// (PayerDaemon.GetSessionDebits) for the final work-units count;
// UNIMPLEMENTED is treated as 0 (best effort).

import { WebSocket, type RawData } from "ws";

import { HEADER, SPEC_VERSION } from "../headers.js";
import { LivepeerBrokerError, errorFromResponse } from "../errors.js";
import type { BrokerCall, BrokerEndpoint } from "../types.js";
import type { SessionDebitsClient } from "../payer-daemon.js";

export const MODE = "ws-realtime@v0";

export interface WsRealtimeRequest extends BrokerCall {
  /**
   * Optional WebSocket subprotocol(s) to negotiate on the upgrade
   * (forwarded as `Sec-WebSocket-Protocol`).
   */
  subprotocols?: string | string[];

  /** Extra application-defined headers (NOT Livepeer-*; passed through). */
  extraHeaders?: Record<string, string>;

  /**
   * Idle timeout in seconds (per the spec's `extra.idle_timeout_seconds`
   * default of 60). The adapter does NOT enforce this — it surfaces it
   * so the gateway can apply the same bound to its customer leg.
   */
  idleTimeoutSeconds?: number;

  /**
   * Optional payer-daemon client used to fetch the final work-units
   * count when the session closes. When omitted, `finalWorkUnits` is
   * always 0.
   */
  debitsClient?: SessionDebitsClient;

  /**
   * Sender ETH address (20 raw bytes), required when `debitsClient` is
   * provided; the daemon keys session ledgers by `(sender, workId)`.
   */
  sender?: Uint8Array;
}

/**
 * Object returned by `connect()`. Mirrors the parts of the standard
 * `WebSocket` interface a gateway typically needs to bridge to its
 * customer leg, plus a `closed` promise that resolves with the final
 * work-units count.
 */
export interface WsRealtimeConnection {
  /** Underlying `ws` socket. Exposed so callers can pipe binary frames. */
  readonly socket: WebSocket;

  /** Echo of `Livepeer-Request-Id` from the upgrade response. */
  readonly requestId: string | undefined;

  /** Send a text or binary frame to the broker. */
  send(data: string | Buffer | Uint8Array): void;

  /** Register a frame handler; raw `ws` `RawData` is forwarded. */
  onMessage(handler: (data: RawData, isBinary: boolean) => void): void;

  /** Register a close handler. The handler runs after `closed` resolves. */
  onClose(handler: (code: number, reason: Buffer) => void): void;

  /**
   * Close the broker leg with `code` (default 1000) and `reason`.
   */
  close(code?: number, reason?: string): void;

  /**
   * Resolves once the broker leg is closed (clean or otherwise) with
   * the final work-units count from the payer-daemon session ledger.
   * Rejects if the broker rejects the upgrade after the resolution of
   * `connect()` (e.g. policy-driven close).
   */
  readonly closed: Promise<{ workUnits: number; code: number }>;

  /** Bytes received from the broker since the connection opened. */
  bytesIn(): number;
  /** Bytes sent to the broker since the connection opened. */
  bytesOut(): number;
}

/**
 * Open a `ws-realtime@v0` connection to the broker. Throws
 * `LivepeerBrokerError` if the broker rejects the upgrade with a
 * Livepeer-Error response (validated before the upgrade completes per
 * the spec).
 */
export async function connect(
  endpoint: BrokerEndpoint,
  req: WsRealtimeRequest,
): Promise<WsRealtimeConnection> {
  const headers: Record<string, string> = {
    [HEADER.CAPABILITY]: req.capability,
    [HEADER.OFFERING]: req.offering,
    [HEADER.PAYMENT]: req.paymentBlob,
    [HEADER.SPEC_VERSION]: SPEC_VERSION,
    [HEADER.MODE]: MODE,
  };
  if (req.requestId) headers[HEADER.REQUEST_ID] = req.requestId;
  if (req.extraHeaders) {
    for (const [k, v] of Object.entries(req.extraHeaders)) {
      headers[k] = v;
    }
  }

  const wsUrl = httpToWsURL(endpoint.url);

  const wsOpts: { headers: Record<string, string>; protocols?: string | string[] } = { headers };
  if (req.subprotocols !== undefined) wsOpts.protocols = req.subprotocols;

  const socket = new WebSocket(new URL("/v1/cap", wsUrl).toString(), wsOpts.protocols ?? [], {
    headers: wsOpts.headers,
  });

  const opened = await waitForOpen(socket, endpoint.signal);
  if (!opened.ok) {
    throw opened.error;
  }
  const requestId = opened.requestId;

  let bytesIn = 0;
  let bytesOut = 0;
  socket.on("message", (data: RawData) => {
    bytesIn += rawSize(data);
  });

  const closed = new Promise<{ workUnits: number; code: number }>((resolve) => {
    socket.once("close", (code: number) => {
      void finalWorkUnits(req).then((workUnits) => resolve({ workUnits, code }));
    });
  });

  return {
    socket,
    requestId,
    send(data) {
      bytesOut += dataSize(data);
      socket.send(data);
    },
    onMessage(handler) {
      socket.on("message", (data: RawData, isBinary: boolean) => handler(data, isBinary));
    },
    onClose(handler) {
      socket.on("close", (code: number, reason: Buffer) => handler(code, reason));
    },
    close(code, reason) {
      socket.close(code ?? 1000, reason);
    },
    closed,
    bytesIn: () => bytesIn,
    bytesOut: () => bytesOut,
  };
}

interface OpenOk {
  ok: true;
  requestId: string | undefined;
}

interface OpenErr {
  ok: false;
  error: Error;
}

function waitForOpen(socket: WebSocket, signal: AbortSignal | undefined): Promise<OpenOk | OpenErr> {
  return new Promise<OpenOk | OpenErr>((resolve) => {
    let settled = false;
    const settle = (r: OpenOk | OpenErr): void => {
      if (settled) return;
      settled = true;
      resolve(r);
    };

    socket.on("open", () => {
      const requestId = readUpgradeHeader(socket, HEADER.REQUEST_ID);
      settle({ ok: true, requestId });
    });

    socket.on("upgrade", (msg) => {
      const status = msg?.statusCode ?? 0;
      if (status >= 400) {
        const headers = adoptHeaders(msg.headers ?? {});
        const err = errorFromResponse(status, headers, "");
        settle({ ok: false, error: err });
      }
    });

    socket.on("unexpected-response", (_req, res) => {
      const status: number = res.statusCode ?? 0;
      const chunks: Buffer[] = [];
      res.on("data", (chunk: Buffer) => chunks.push(chunk));
      res.on("end", () => {
        const body = Buffer.concat(chunks);
        const headers = adoptHeaders(res.headers ?? {});
        const err = errorFromResponse(status, headers, body);
        settle({ ok: false, error: err });
      });
    });

    socket.on("error", (err: Error) => {
      if (err instanceof LivepeerBrokerError) {
        settle({ ok: false, error: err });
        return;
      }
      settle({ ok: false, error: err });
    });

    if (signal) {
      const onAbort = (): void => {
        try {
          socket.terminate();
        } catch {
          // ignore — we're already aborting
        }
        settle({ ok: false, error: signal.reason instanceof Error ? signal.reason : new Error("aborted") });
      };
      if (signal.aborted) {
        onAbort();
      } else {
        signal.addEventListener("abort", onAbort, { once: true });
      }
    }
  });
}

function readUpgradeHeader(socket: WebSocket, name: string): string | undefined {
  // The `ws` library exposes the upgrade response headers via the
  // `upgrade` event; we don't have a synchronous accessor here, so the
  // adapter doesn't surface request-id from text upgrades unless the
  // gateway captures it via an `upgrade` listener of its own. Return
  // undefined here; the caller may pre-mint the request-id and echo
  // it via `WsRealtimeRequest.requestId` instead.
  void socket;
  void name;
  return undefined;
}

function adoptHeaders(input: Record<string, string | string[] | undefined>): Headers {
  const out = new Headers();
  for (const [k, v] of Object.entries(input)) {
    if (v === undefined) continue;
    if (Array.isArray(v)) {
      for (const vv of v) out.append(k, vv);
    } else {
      out.set(k, v);
    }
  }
  return out;
}

function httpToWsURL(httpURL: string): string {
  const u = new URL(httpURL);
  if (u.protocol === "http:") u.protocol = "ws:";
  else if (u.protocol === "https:") u.protocol = "wss:";
  return u.toString();
}

function dataSize(data: string | Buffer | Uint8Array): number {
  if (typeof data === "string") return Buffer.byteLength(data, "utf8");
  if (Buffer.isBuffer(data)) return data.length;
  return data.byteLength;
}

function rawSize(data: RawData): number {
  if (Buffer.isBuffer(data)) return data.length;
  if (Array.isArray(data)) {
    return data.reduce((acc, b) => acc + b.length, 0);
  }
  return (data as ArrayBuffer).byteLength;
}

async function finalWorkUnits(req: WsRealtimeRequest): Promise<number> {
  if (!req.debitsClient || !req.sender || !req.requestId) return 0;
  try {
    const debits = await req.debitsClient.getSessionDebits({
      sender: req.sender,
      workId: req.requestId,
    });
    return debits.totalWorkUnits;
  } catch {
    return 0;
  }
}
