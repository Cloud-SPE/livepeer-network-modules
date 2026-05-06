// http-multipart@v0 middleware per
// livepeer-network-protocol/modes/http-multipart.md.
//
// Multipart/form-data request body → regular HTTP response. The wire
// shape is identical to http-reqresp from the gateway's perspective,
// modulo the request body construction. The send() function here
// accepts a pre-built FormData (or a literal Buffer/string) and forwards
// it; building the multipart payload is the caller's responsibility
// (FormData with @types/node is sufficient in Node 18+).

import { HEADER, SPEC_VERSION } from "../headers.js";
import { errorFromResponse } from "../errors.js";
import type { BrokerCall, BrokerEndpoint, BrokerResponseEnvelope } from "../types.js";

/** The canonical mode-name@vN string this middleware implements. */
export const MODE = "http-multipart@v0";

/** Inputs unique to http-multipart. */
export interface HttpMultipartRequest extends BrokerCall {
  /**
   * Either a FormData (recommended; Node sets the Content-Type with the
   * boundary automatically) or a literal Buffer / string with a
   * matching Content-Type that includes the boundary parameter.
   */
  body: FormData | Buffer | Uint8Array | string;
  /**
   * Required when body is not a FormData; must include the boundary
   * parameter. Ignored when body is a FormData (fetch sets it).
   */
  contentType?: string;
  extraHeaders?: Record<string, string>;
}

/** Response shape returned by the http-multipart middleware. */
export interface HttpMultipartResponse extends BrokerResponseEnvelope {
  body: ArrayBuffer;
  headers: Headers;
}

export async function send(
  endpoint: BrokerEndpoint,
  req: HttpMultipartRequest,
): Promise<HttpMultipartResponse> {
  const headers = new Headers();
  headers.set(HEADER.CAPABILITY, req.capability);
  headers.set(HEADER.OFFERING, req.offering);
  headers.set(HEADER.PAYMENT, req.paymentBlob);
  headers.set(HEADER.SPEC_VERSION, SPEC_VERSION);
  headers.set(HEADER.MODE, MODE);
  if (req.requestId) headers.set(HEADER.REQUEST_ID, req.requestId);

  // FormData: let fetch set Content-Type with the multipart boundary.
  // Buffer / string / Uint8Array: caller MUST pass a matching contentType.
  if (!(req.body instanceof FormData)) {
    if (!req.contentType) {
      throw new Error(
        "http-multipart: contentType is required when body is not a FormData (must include the multipart boundary)",
      );
    }
    headers.set("Content-Type", req.contentType);
  }
  if (req.extraHeaders) {
    for (const [k, v] of Object.entries(req.extraHeaders)) {
      headers.set(k, v);
    }
  }

  // BodyInit accepts FormData, Buffer (Uint8Array), and string directly.
  const fetchBody: BodyInit =
    req.body instanceof FormData
      ? req.body
      : (req.body as BodyInit);

  const url = new URL("/v1/cap", endpoint.url).toString();
  const resp = await fetch(url, {
    method: "POST",
    headers,
    body: fetchBody,
    signal: endpoint.signal,
  });

  const respBody = await resp.arrayBuffer();
  const requestId = resp.headers.get(HEADER.REQUEST_ID) ?? undefined;

  if (resp.status >= 400) {
    throw errorFromResponse(resp.status, resp.headers, respBody);
  }

  const workUnitsRaw = resp.headers.get(HEADER.WORK_UNITS);
  const workUnits = workUnitsRaw ? parseInt(workUnitsRaw, 10) || 0 : 0;

  return {
    status: resp.status,
    body: respBody,
    headers: resp.headers,
    workUnits,
    requestId,
  };
}
