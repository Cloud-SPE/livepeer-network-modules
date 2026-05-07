// Worker-control bearer — passed to the runner in the
// `POST /api/sessions/start` body and used by the runner to verify the
// gateway's origin on subsequent control-WS handshakes.
//
// Per Q8 lock the bearer is HMAC-SHA-256 with a deployment-wide pepper;
// hash-stored on the gateway side, plain-text in transit (over TLS).
// Format `vtbsw_<43-char-base64url>`. Suite source surface:
// `livepeer-network-suite/livepeer-vtuber-gateway/src/service/auth/
// workerControlBearer.ts`.

import { createHmac, randomBytes } from "node:crypto";

const SECRET_BYTES = 32;
const PREFIX = "vtbsw_";
const ENCODED_SECRET_LEN = 43;

export interface MintedWorkerControlBearer {
  bearer: string;
  hash: string;
}

export function mintWorkerControlBearer(
  pepper: string,
): MintedWorkerControlBearer {
  if (pepper.length < 16) {
    throw new Error("worker-control bearer pepper must be at least 16 chars");
  }
  const secret = randomBytes(SECRET_BYTES).toString("base64url");
  const bearer = `${PREFIX}${secret}`;
  const hash = hashWorkerControlBearer(bearer, pepper);
  return { bearer, hash };
}

export function hashWorkerControlBearer(
  bearer: string,
  pepper: string,
): string {
  if (!bearer.startsWith(PREFIX)) {
    throw new Error("worker-control bearer must start with vtbsw_");
  }
  const secret = bearer.slice(PREFIX.length);
  if (secret.length !== ENCODED_SECRET_LEN) {
    throw new Error("worker-control bearer secret length mismatch");
  }
  return createHmac("sha256", pepper).update(secret).digest("hex");
}
