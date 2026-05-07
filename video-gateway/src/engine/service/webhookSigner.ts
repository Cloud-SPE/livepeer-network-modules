import { createHmac, timingSafeEqual } from "node:crypto";

export interface SignedHeaders {
  "X-Livepeer-Signature": string;
  "X-Livepeer-Timestamp": string;
  "X-Livepeer-Event-Type": string;
  "X-Livepeer-Delivery-Id": string;
  "Content-Type": string;
}

export function signEvent(opts: {
  secret: string;
  body: string;
  eventType: string;
  deliveryId: string;
  timestamp?: number;
}): SignedHeaders {
  const ts = opts.timestamp ?? Math.floor(Date.now() / 1000);
  const signedPayload = `${ts}.${opts.body}`;
  const hex = createHmac("sha256", opts.secret).update(signedPayload).digest("hex");
  return {
    "X-Livepeer-Signature": `sha256=${hex}`,
    "X-Livepeer-Timestamp": String(ts),
    "X-Livepeer-Event-Type": opts.eventType,
    "X-Livepeer-Delivery-Id": opts.deliveryId,
    "Content-Type": "application/json",
  };
}

export function verifyEvent(opts: {
  secret: string;
  body: string;
  signature: string;
  timestamp: string;
  toleranceSec?: number;
  now?: number;
}): boolean {
  const tolerance = opts.toleranceSec ?? 300;
  const now = opts.now ?? Math.floor(Date.now() / 1000);
  const ts = parseInt(opts.timestamp, 10);
  if (Number.isNaN(ts)) return false;
  const ageSec = now - ts;
  if (ageSec > tolerance || ageSec < -60) return false;

  const expected =
    "sha256=" +
    createHmac("sha256", opts.secret).update(`${ts}.${opts.body}`).digest("hex");

  const sigBuf = Buffer.from(opts.signature);
  const expBuf = Buffer.from(expected);
  if (sigBuf.length !== expBuf.length) return false;
  return timingSafeEqual(sigBuf, expBuf);
}
