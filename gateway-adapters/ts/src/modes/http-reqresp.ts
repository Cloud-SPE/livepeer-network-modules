// http-reqresp@v0 middleware per
// livepeer-network-protocol/modes/http-reqresp.md.
//
// One HTTP request → one HTTP response. Livepeer-Work-Units in the
// regular response header slot. Uses Node's `fetch`.

import { HEADER, SPEC_VERSION } from "../headers.js";
import { errorFromResponse } from "../errors.js";
import type { BrokerCall, BrokerEndpoint, BrokerResponseEnvelope } from "../types.js";

/** The canonical mode-name@vN string this middleware implements. */
export const MODE = "http-reqresp@v0";

/** Inputs unique to http-reqresp (in addition to BrokerCall). */
export interface HttpReqRespRequest extends BrokerCall {
  /** Application-defined request body. */
  body: BodyInit | null;
  /** Content-Type for the request body (e.g. `application/json`). */
  contentType?: string;
  /** Extra application-defined headers (NOT Livepeer-*; will be passed through). */
  extraHeaders?: Record<string, string>;
}

/** Response shape returned by the http-reqresp middleware. */
export interface HttpReqRespResponse extends BrokerResponseEnvelope {
  /** Backend response body bytes (passed through unchanged by the broker). */
  body: ArrayBuffer;
  /** All response headers. */
  headers: Headers;
}

/**
 * Send one paid request through a broker using the http-reqresp@v0 wire
 * shape. Throws `LivepeerBrokerError` on non-2xx responses.
 */
export async function send(
  endpoint: BrokerEndpoint,
  req: HttpReqRespRequest,
): Promise<HttpReqRespResponse> {
  const headers = new Headers();
  headers.set(HEADER.CAPABILITY, req.capability);
  headers.set(HEADER.OFFERING, req.offering);
  headers.set(HEADER.PAYMENT, req.paymentBlob);
  headers.set(HEADER.SPEC_VERSION, SPEC_VERSION);
  headers.set(HEADER.MODE, MODE);
  if (req.requestId) headers.set(HEADER.REQUEST_ID, req.requestId);
  if (req.contentType) headers.set("Content-Type", req.contentType);
  if (req.extraHeaders) {
    for (const [k, v] of Object.entries(req.extraHeaders)) {
      headers.set(k, v);
    }
  }

  const url = new URL("/v1/cap", endpoint.url).toString();
  const resp = await fetch(url, {
    method: "POST",
    headers,
    body: req.body,
    signal: endpoint.signal,
  });

  const respBody = await resp.arrayBuffer();
  const requestId = resp.headers.get(HEADER.REQUEST_ID) ?? undefined;

  if (resp.status >= 400) {
    throw errorFromResponse(resp.status, resp.headers, respBody);
  }

  const workUnits = parseWorkUnits(resp.headers.get(HEADER.WORK_UNITS));

  return {
    status: resp.status,
    body: respBody,
    headers: resp.headers,
    workUnits,
    requestId,
  };
}

function parseWorkUnits(raw: string | null): number {
  if (!raw) return 0;
  const n = parseInt(raw, 10);
  return Number.isNaN(n) ? 0 : n;
}
