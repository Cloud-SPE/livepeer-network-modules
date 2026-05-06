import * as http from "node:http";
import * as https from "node:https";
import { HEADER, SPEC_VERSION } from "./headers.js";
import { errorFromResponse } from "./errors.js";

export const MODE = "http-stream@v0";

export interface SendOpts {
  brokerUrl: string;
  capability: string;
  offering: string;
  paymentBlob: string;
  body: string | Buffer | null;
  contentType?: string;
  requestId?: string;
  signal?: AbortSignal;
}

export interface SendResult {
  status: number;
  body: Buffer;
  headers: Record<string, string | string[] | undefined>;
  trailers: Record<string, string>;
  workUnits: number;
  requestId: string | undefined;
}

export function send(opts: SendOpts): Promise<SendResult> {
  const requestHeaders: Record<string, string> = {
    [HEADER.CAPABILITY]: opts.capability,
    [HEADER.OFFERING]: opts.offering,
    [HEADER.PAYMENT]: opts.paymentBlob,
    [HEADER.SPEC_VERSION]: SPEC_VERSION,
    [HEADER.MODE]: MODE,
    Accept: "text/event-stream",
  };
  if (opts.requestId) requestHeaders[HEADER.REQUEST_ID] = opts.requestId;
  if (opts.contentType) requestHeaders["Content-Type"] = opts.contentType;

  const url = new URL("/v1/cap", opts.brokerUrl);
  const isHttps = url.protocol === "https:";
  const transport = isHttps ? https : http;

  return new Promise<SendResult>((resolve, reject) => {
    const req = transport.request(
      {
        method: "POST",
        hostname: url.hostname,
        port: url.port || (isHttps ? 443 : 80),
        path: url.pathname + url.search,
        headers: requestHeaders,
        signal: opts.signal,
      },
      (resp) => {
        const chunks: Buffer[] = [];
        resp.on("data", (chunk: Buffer) => chunks.push(chunk));
        resp.on("end", () => {
          const respBody = Buffer.concat(chunks);
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

          let workUnits = 0;
          const trailerVal = trailerMap[HEADER.WORK_UNITS.toLowerCase()];
          const headerRaw = resp.headers[HEADER.WORK_UNITS.toLowerCase()];
          const headerVal = Array.isArray(headerRaw) ? headerRaw[0] : headerRaw;
          if (trailerVal) workUnits = parseInt(trailerVal, 10) || 0;
          else if (headerVal) workUnits = parseInt(headerVal, 10) || 0;

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
    req.on("error", reject);
    if (opts.body !== null && opts.body !== undefined) req.write(opts.body);
    req.end();
  });
}
