import { HEADER } from "./headers.js";

/**
 * Error thrown when the broker returns a non-2xx response or the response
 * is malformed. Carries the structured Livepeer-Error code, optional
 * Livepeer-Backoff advice, and the echoed Livepeer-Request-Id when
 * available.
 */
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

/**
 * Build a LivepeerBrokerError from a non-2xx Response + body bytes.
 * Decodes the body as UTF-8, attempts to parse JSON for a `message`
 * field, and reads the standard Livepeer-* response headers.
 */
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

  let bodyStr: string;
  if (typeof body === "string") {
    bodyStr = body;
  } else if (body instanceof Uint8Array) {
    bodyStr = new TextDecoder().decode(body);
  } else {
    bodyStr = new TextDecoder().decode(body);
  }

  let message = `broker error: ${code}`;
  try {
    const parsed = JSON.parse(bodyStr) as { message?: unknown };
    if (typeof parsed.message === "string") {
      message = parsed.message;
    }
  } catch {
    // body is not JSON; fall back to the default message
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
