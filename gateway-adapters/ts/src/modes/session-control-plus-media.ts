// session-control-plus-media@v0 middleware per
// livepeer-network-protocol/modes/session-control-plus-media.md.
//
// Two surfaces in the TS half:
//   1. `openSession()` — POST /v1/cap session-open (HTTP-reqresp-shaped).
//   2. `connectControl()` — WebSocket connection to the broker-issued
//      `control_url`, surfaced as a strongly-typed event emitter.
//
// The media plane (WebRTC SFU pass-through, RTMP, trickle, etc.) is
// capability-defined and the protocol does NOT interpret the `media`
// descriptor. WebRTC media-plane mediation lives in the Go half at
// gateway-adapters/go/modes/sessioncontrolplusmedia/.

import { EventEmitter } from "node:events";
import { WebSocket, type RawData } from "ws";

import { HEADER, SPEC_VERSION } from "../headers.js";
import { LivepeerBrokerError, errorFromResponse } from "../errors.js";
import type { BrokerCall, BrokerEndpoint, BrokerResponseEnvelope } from "../types.js";
import type { SessionDebitsClient } from "../payer-daemon.js";

export const MODE = "session-control-plus-media@v0";

export interface SessionOpenRequest extends BrokerCall {
  /** Capability-defined JSON body for session-open. */
  body: unknown;
  /** Extra application-defined headers (NOT Livepeer-*; passed through). */
  extraHeaders?: Record<string, string>;
}

export interface SessionDescriptor extends BrokerResponseEnvelope {
  sessionId: string;
  controlUrl: string;
  /** Capability-defined media-plane descriptor; opaque to the protocol. */
  media: Record<string, unknown> | null;
  expiresAt: string;
  /** All response headers from the session-open call. */
  headers: Headers;
}

/**
 * Inbound control-plane events the protocol defines. Capability-specific
 * messages forward via the generic `event` slot.
 */
export interface ControlPlaneEvents {
  "session.started": (payload: Record<string, unknown>) => void;
  "session.balance.low": (payload: Record<string, unknown>) => void;
  "session.balance.refilled": (payload: Record<string, unknown>) => void;
  "session.usage.tick": (payload: Record<string, unknown>) => void;
  "session.error": (payload: Record<string, unknown>) => void;
  "session.ended": (payload: Record<string, unknown>) => void;
  message: (raw: RawData, isBinary: boolean) => void;
  close: (code: number, reason: Buffer) => void;
  error: (err: Error) => void;
}

export interface ControlPlaneConnection {
  /** Underlying `ws` socket, exposed for callers that need it. */
  readonly socket: WebSocket;

  /** Subscribe to a typed control-plane event. */
  on<K extends keyof ControlPlaneEvents>(event: K, listener: ControlPlaneEvents[K]): void;
  off<K extends keyof ControlPlaneEvents>(event: K, listener: ControlPlaneEvents[K]): void;

  /**
   * Send a structured outbound message — `session.end` is recognised by
   * the protocol; other messages are capability-defined and forwarded
   * verbatim.
   */
  send(message: { type: string } & Record<string, unknown>): void;

  /** Send a raw frame the adapter does not interpret. */
  sendRaw(data: string | Buffer | Uint8Array): void;

  /** Initiate a clean close of the control WS. */
  close(code?: number, reason?: string): void;

  /**
   * Resolves once the control WS closes (clean or otherwise) with the
   * final work-units count from the payer-daemon session ledger.
   */
  readonly closed: Promise<{ workUnits: number; code: number }>;
}

export interface ControlPlaneOptions {
  /** Echoed Livepeer-Request-Id, if the gateway minted one for the session. */
  requestId?: string;

  /** Sender ETH address (20 raw bytes), required when `debitsClient` is set. */
  sender?: Uint8Array;

  /** Optional payer-daemon client used for final-debit reporting on close. */
  debitsClient?: SessionDebitsClient;

  /** Optional AbortSignal cancelling the open. */
  signal?: AbortSignal;
}

/**
 * POST /v1/cap session-open. Throws `LivepeerBrokerError` on non-2xx.
 */
export async function openSession(
  endpoint: BrokerEndpoint,
  req: SessionOpenRequest,
): Promise<SessionDescriptor> {
  const headers = new Headers();
  headers.set(HEADER.CAPABILITY, req.capability);
  headers.set(HEADER.OFFERING, req.offering);
  headers.set(HEADER.PAYMENT, req.paymentBlob);
  headers.set(HEADER.SPEC_VERSION, SPEC_VERSION);
  headers.set(HEADER.MODE, MODE);
  headers.set("Content-Type", "application/json");
  if (req.requestId) headers.set(HEADER.REQUEST_ID, req.requestId);
  if (req.extraHeaders) {
    for (const [k, v] of Object.entries(req.extraHeaders)) {
      headers.set(k, v);
    }
  }

  const url = new URL("/v1/cap", endpoint.url).toString();
  const resp = await fetch(url, {
    method: "POST",
    headers,
    body: JSON.stringify(req.body ?? {}),
    signal: endpoint.signal,
  });

  const respBody = await resp.arrayBuffer();
  const requestId = resp.headers.get(HEADER.REQUEST_ID) ?? undefined;

  if (resp.status >= 400) {
    throw errorFromResponse(resp.status, resp.headers, respBody);
  }

  const text = new TextDecoder().decode(respBody);
  let parsed: {
    session_id?: unknown;
    control_url?: unknown;
    media?: unknown;
    expires_at?: unknown;
  };
  try {
    parsed = JSON.parse(text) as Record<string, unknown>;
  } catch (err) {
    throw new LivepeerBrokerError({
      status: resp.status,
      code: "internal_error",
      message: `session-open response not JSON: ${(err as Error).message}`,
      requestId,
      responseBody: text,
    });
  }

  const sessionId = strict(parsed.session_id, "session_id");
  const controlUrl = strict(parsed.control_url, "control_url");
  const expiresAt = strict(parsed.expires_at, "expires_at");
  const media = isObject(parsed.media) ? (parsed.media as Record<string, unknown>) : null;

  const workUnits = parseWorkUnits(resp.headers.get(HEADER.WORK_UNITS));

  return {
    sessionId,
    controlUrl,
    media,
    expiresAt,
    status: resp.status,
    workUnits,
    requestId,
    headers: resp.headers,
  };
}

/**
 * Open the broker-issued `control_url` and return a typed event-emitter
 * wrapper.
 */
export async function connectControl(
  controlUrl: string,
  options: ControlPlaneOptions = {},
): Promise<ControlPlaneConnection> {
  const headers: Record<string, string> = {};
  if (options.requestId) headers[HEADER.REQUEST_ID] = options.requestId;

  const socket = new WebSocket(controlUrl, { headers });
  const emitter = new EventEmitter();
  const state = { drained: false };
  const buffered: Array<{ event: string; args: unknown[] }> = [];

  const dispatch = (event: string, args: unknown[]): void => {
    if (state.drained) {
      emitter.emit(event, ...args);
      return;
    }
    buffered.push({ event, args });
  };

  socket.on("message", (data: RawData, isBinary: boolean) => {
    if (!isBinary) {
      const evt = parseEvent(data);
      if (evt && KNOWN_EVENTS.has(evt.type)) {
        dispatch(evt.type, [evt.payload]);
        return;
      }
    }
    dispatch("message", [data, isBinary]);
  });
  socket.on("close", (code: number, reason: Buffer) => dispatch("close", [code, reason]));
  socket.on("error", (err: Error) => dispatch("error", [err]));

  const closed = new Promise<{ workUnits: number; code: number }>((resolve) => {
    socket.once("close", (code: number) => {
      void finalWorkUnits(options).then((workUnits) => resolve({ workUnits, code }));
    });
  });

  const opened = await waitForOpen(socket, options.signal);
  if (!opened.ok) throw opened.error;

  // Buffered frames flush after the caller attaches their first
  // listener, so the broker's session.started message (typically
  // arriving before the caller's handler is attached) is observed.
  const armDrain = (): void => {
    if (state.drained) return;
    state.drained = true;
    queueMicrotask(() => {
      for (const { event, args } of buffered) {
        emitter.emit(event, ...args);
      }
      buffered.length = 0;
    });
  };

  return {
    socket,
    on: <K extends keyof ControlPlaneEvents>(event: K, listener: ControlPlaneEvents[K]): void => {
      emitter.on(event, listener as (...args: unknown[]) => void);
      armDrain();
    },
    off: <K extends keyof ControlPlaneEvents>(event: K, listener: ControlPlaneEvents[K]): void => {
      emitter.off(event, listener as (...args: unknown[]) => void);
    },
    send(message) {
      socket.send(JSON.stringify(message));
    },
    sendRaw(data) {
      socket.send(data);
    },
    close(code, reason) {
      socket.close(code ?? 1000, reason);
    },
    closed,
  };
}

const KNOWN_EVENTS: ReadonlySet<string> = new Set([
  "session.started",
  "session.balance.low",
  "session.balance.refilled",
  "session.usage.tick",
  "session.error",
  "session.ended",
]);

function parseEvent(data: RawData): { type: string; payload: Record<string, unknown> } | null {
  let text: string;
  if (Buffer.isBuffer(data)) text = data.toString("utf8");
  else if (Array.isArray(data)) text = Buffer.concat(data).toString("utf8");
  else if (data instanceof ArrayBuffer) text = Buffer.from(data).toString("utf8");
  else return null;

  try {
    const parsed = JSON.parse(text) as { type?: unknown; [k: string]: unknown };
    if (typeof parsed.type !== "string") return null;
    return { type: parsed.type, payload: parsed };
  } catch {
    return null;
  }
}

interface OpenOk {
  ok: true;
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
    socket.once("open", () => settle({ ok: true }));
    socket.once("unexpected-response", (_req, res) => {
      const status: number = res.statusCode ?? 0;
      const chunks: Buffer[] = [];
      res.on("data", (c: Buffer) => chunks.push(c));
      res.on("end", () => {
        const headers = adoptHeaders(res.headers ?? {});
        settle({
          ok: false,
          error: errorFromResponse(status, headers, Buffer.concat(chunks)),
        });
      });
    });
    socket.once("error", (err: Error) => settle({ ok: false, error: err }));
    if (signal) {
      const onAbort = (): void => {
        try {
          socket.terminate();
        } catch {
          // ignore
        }
        settle({
          ok: false,
          error: signal.reason instanceof Error ? signal.reason : new Error("aborted"),
        });
      };
      if (signal.aborted) onAbort();
      else signal.addEventListener("abort", onAbort, { once: true });
    }
  });
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

function isObject(v: unknown): boolean {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function strict(v: unknown, name: string): string {
  if (typeof v !== "string" || v === "") {
    throw new LivepeerBrokerError({
      status: 200,
      code: "internal_error",
      message: `session-open response missing ${name}`,
    });
  }
  return v;
}

function parseWorkUnits(raw: string | null): number {
  if (!raw) return 0;
  const n = parseInt(raw, 10);
  return Number.isNaN(n) ? 0 : n;
}

async function finalWorkUnits(options: ControlPlaneOptions): Promise<number> {
  if (!options.debitsClient || !options.sender || !options.requestId) return 0;
  try {
    const debits = await options.debitsClient.getSessionDebits({
      sender: options.sender,
      workId: options.requestId,
    });
    return debits.totalWorkUnits;
  } catch {
    return 0;
  }
}
