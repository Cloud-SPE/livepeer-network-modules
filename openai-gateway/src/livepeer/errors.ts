import { HEADER } from "./headers.js";

export class LivepeerBrokerError extends Error {
  readonly status: number;
  readonly code: string;
  readonly backoffSeconds: number | undefined;
  readonly requestId: string | undefined;
  readonly responseBody: string;

  constructor(opts: {
    status: number;
    code: string;
    message: string;
    backoffSeconds?: number;
    requestId?: string;
    responseBody?: string;
  }) {
    super(opts.message);
    this.name = "LivepeerBrokerError";
    this.status = opts.status;
    this.code = opts.code;
    this.backoffSeconds = opts.backoffSeconds;
    this.requestId = opts.requestId;
    this.responseBody = opts.responseBody ?? "";
  }
}

export function isInvalidRecipientRandPaymentError(err: unknown): err is LivepeerBrokerError {
  if (!(err instanceof LivepeerBrokerError)) return false;
  if (err.code !== "payment_invalid") return false;
  return err.responseBody.includes("INVALID_RECIPIENT_RAND") || err.message.includes("INVALID_RECIPIENT_RAND");
}

// gatewayHttpStatusFor maps a broker-side error to the HTTP status the
// gateway should surface to the customer. Upstream 5xx is squashed to
// 502 because from the customer's perspective the gateway's backend is
// bad. The carve-out: `broker_quote_rejected` keeps its 503 because the
// rejection is an explicit contract decision (resolver or payer-daemon
// refused to quote this route), not a transient backend failure — so
// the customer-facing semantics are "service unavailable for this
// model", not "upstream gave us a 5xx".
export function gatewayHttpStatusFor(err: LivepeerBrokerError): number {
  if (err.code === "broker_quote_rejected") return err.status;
  return err.status >= 500 ? 502 : err.status;
}

export function errorFromResponse(
  status: number,
  headers: Headers | Record<string, string | string[] | undefined>,
  body: ArrayBuffer | Uint8Array | string,
): LivepeerBrokerError {
  const get = (name: string): string | undefined => {
    if (headers instanceof Headers) {
      return headers.get(name) ?? undefined;
    }
    const raw = (headers as Record<string, string | string[] | undefined>)[name.toLowerCase()];
    if (Array.isArray(raw)) return raw[0];
    return raw;
  };

  const code = get(HEADER.ERROR) ?? "unknown";
  const requestId = get(HEADER.REQUEST_ID);
  const backoffStr = get(HEADER.BACKOFF);
  let backoffSeconds: number | undefined;
  if (backoffStr) {
    const n = parseInt(backoffStr, 10);
    if (!Number.isNaN(n)) backoffSeconds = n;
  }

  const bodyStr =
    typeof body === "string"
      ? body
      : new TextDecoder().decode(body instanceof Uint8Array ? body : body);

  let message = `broker error: ${code}`;
  try {
    const parsed = JSON.parse(bodyStr) as { message?: unknown };
    if (typeof parsed.message === "string") message = parsed.message;
  } catch {
    // body not JSON
  }

  return new LivepeerBrokerError({
    status,
    code,
    message,
    backoffSeconds,
    requestId,
    responseBody: bodyStr,
  });
}
