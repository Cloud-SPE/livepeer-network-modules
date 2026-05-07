// http-stream@v0 middleware per
// livepeer-network-protocol/modes/http-stream.md.
//
// One HTTP request → streaming (SSE / chunked) response. Livepeer-Work-
// Units is reported as an HTTP trailer, not a regular header. The
// standard `fetch` API does not expose response trailers, so this
// middleware uses Node's built-in `node:http` / `node:https` modules
// (zero runtime dependencies).

import * as http from "node:http";
import * as https from "node:https";

import { HEADER, SPEC_VERSION } from "../headers.js";
import { errorFromResponse } from "../errors.js";
import type { BrokerCall, BrokerEndpoint, BrokerResponseEnvelope } from "../types.js";

/** The canonical mode-name@vN string this middleware implements. */
export const MODE = "http-stream@v0";

/** Inputs unique to http-stream. */
export interface HttpStreamRequest extends BrokerCall {
  body: string | Buffer | Uint8Array | null;
  contentType?: string;
  extraHeaders?: Record<string, string>;
  /** Override Accept; default `text/event-stream`. */
  accept?: string;
}

/** Response shape returned by the http-stream middleware. */
export interface HttpStreamResponse extends BrokerResponseEnvelope {
  /** Backend response body bytes, fully read (necessary to populate trailers). */
  body: Buffer;
  /** Response headers (lowercase keys per Node's IncomingMessage convention). */
  headers: Record<string, string | string[] | undefined>;
  /** Response trailers (Livepeer-Work-Units lives here per the mode spec). */
  trailers: Record<string, string>;
}

export async function send(
  endpoint: BrokerEndpoint,
  req: HttpStreamRequest,
): Promise<HttpStreamResponse> {
  const requestHeaders: Record<string, string> = {
    [HEADER.CAPABILITY]: req.capability,
    [HEADER.OFFERING]: req.offering,
    [HEADER.PAYMENT]: req.paymentBlob,
    [HEADER.SPEC_VERSION]: SPEC_VERSION,
    [HEADER.MODE]: MODE,
    Accept: req.accept ?? "text/event-stream",
  };
  if (req.requestId) requestHeaders[HEADER.REQUEST_ID] = req.requestId;
  if (req.contentType) requestHeaders["Content-Type"] = req.contentType;
  if (req.extraHeaders) {
    for (const [k, v] of Object.entries(req.extraHeaders)) {
      requestHeaders[k] = v;
    }
  }

  const url = new URL("/v1/cap", endpoint.url);
  const isHttps = url.protocol === "https:";
  const transport = isHttps ? https : http;

  return new Promise<HttpStreamResponse>((resolve, reject) => {
    const httpReq = transport.request(
      {
        method: "POST",
        hostname: url.hostname,
        port: url.port || (isHttps ? 443 : 80),
        path: url.pathname + url.search,
        headers: requestHeaders,
        signal: endpoint.signal,
      },
      (resp) => {
        const chunks: Buffer[] = [];
        resp.on("data", (chunk: Buffer) => chunks.push(chunk));
        resp.on("end", () => {
          const respBody = Buffer.concat(chunks);
          // resp.trailers is typed as NodeJS.Dict<string> — values are
          // string | undefined, never arrays.
          const trailerMap: Record<string, string> = {};
          for (const [k, v] of Object.entries(resp.trailers)) {
            if (typeof v === "string") trailerMap[k.toLowerCase()] = v;
          }

          const requestIdRaw = resp.headers[HEADER.REQUEST_ID.toLowerCase()];
          const requestId = Array.isArray(requestIdRaw) ? requestIdRaw[0] : requestIdRaw;

          const status = resp.statusCode ?? 0;
          if (status >= 400) {
            reject(errorFromResponse(status, resp.headers, respBody));
            return;
          }

          // Read Livepeer-Work-Units from the trailer slot per spec.
          let workUnits = 0;
          const trailerVal = trailerMap[HEADER.WORK_UNITS.toLowerCase()];
          const headerRaw = resp.headers[HEADER.WORK_UNITS.toLowerCase()];
          const headerVal = Array.isArray(headerRaw) ? headerRaw[0] : headerRaw;
          if (trailerVal) {
            workUnits = parseInt(trailerVal, 10) || 0;
          } else if (headerVal) {
            workUnits = parseInt(headerVal, 10) || 0;
            // eslint-disable-next-line no-console
            console.warn(
              "http-stream@v0: Livepeer-Work-Units in response header instead of trailer (broker non-conformance)",
            );
          }

          resolve({
            status,
            body: respBody,
            headers: resp.headers,
            trailers: trailerMap,
            workUnits,
            requestId,
          });
        });
        resp.on("error", reject);
      },
    );
    httpReq.on("error", reject);
    if (req.body !== null && req.body !== undefined) {
      httpReq.write(req.body);
    }
    httpReq.end();
  });
}
