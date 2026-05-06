import { HEADER, SPEC_VERSION } from "./headers.js";
import { errorFromResponse } from "./errors.js";

export const MODE = "http-multipart@v0";

export interface SendOpts {
  brokerUrl: string;
  capability: string;
  offering: string;
  paymentBlob: string;
  /** FormData (recommended) or a literal multipart body with explicit Content-Type. */
  body: FormData | Buffer | string;
  /** Required when body is not a FormData (must include the boundary). */
  contentType?: string;
  requestId?: string;
  signal?: AbortSignal;
}

export interface SendResult {
  status: number;
  body: ArrayBuffer;
  headers: Headers;
  workUnits: number;
  requestId: string | undefined;
}

export async function send(opts: SendOpts): Promise<SendResult> {
  const headers = new Headers();
  headers.set(HEADER.CAPABILITY, opts.capability);
  headers.set(HEADER.OFFERING, opts.offering);
  headers.set(HEADER.PAYMENT, opts.paymentBlob);
  headers.set(HEADER.SPEC_VERSION, SPEC_VERSION);
  headers.set(HEADER.MODE, MODE);
  if (opts.requestId) headers.set(HEADER.REQUEST_ID, opts.requestId);

  if (!(opts.body instanceof FormData)) {
    if (!opts.contentType) {
      throw new Error("http-multipart: contentType is required when body is not FormData");
    }
    headers.set("Content-Type", opts.contentType);
  }

  const fetchBody: BodyInit = opts.body instanceof FormData ? opts.body : (opts.body as BodyInit);

  const url = new URL("/v1/cap", opts.brokerUrl).toString();
  const resp = await fetch(url, {
    method: "POST",
    headers,
    body: fetchBody,
    signal: opts.signal,
  });

  const respBody = await resp.arrayBuffer();
  const requestId = resp.headers.get(HEADER.REQUEST_ID) ?? undefined;

  if (resp.status >= 400) {
    throw errorFromResponse(resp.status, resp.headers, respBody);
  }

  const wuStr = resp.headers.get(HEADER.WORK_UNITS);
  const workUnits = wuStr ? parseInt(wuStr, 10) || 0 : 0;

  return { status: resp.status, body: respBody, headers: resp.headers, workUnits, requestId };
}
