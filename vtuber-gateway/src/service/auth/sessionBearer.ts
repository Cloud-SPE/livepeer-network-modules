// Customer-side session-scoped child bearer.
//
// Per Q8 lock + the suite's M3 implementation
// (livepeer-network-suite/livepeer-vtuber-gateway/src/service/auth/
// sessionBearer.ts), bearers are HMAC-SHA-256 with a deployment-wide
// pepper, with only the hash stored in `vtuber.session_bearer.hash`.
// Format `vtbs_<43-char-base64url>` (32-byte secret base64url-encoded
// without padding).

import { createHmac, randomBytes, timingSafeEqual } from "node:crypto";

const SECRET_BYTES = 32;
const PREFIX = "vtbs_";
const ENCODED_SECRET_LEN = 43; // 32 bytes base64url (no padding)

export interface MintedSessionBearer {
  bearer: string;
  hash: string;
}

export function mintSessionBearer(pepper: string): MintedSessionBearer {
  if (pepper.length < 16) {
    throw new Error("session-bearer pepper must be at least 16 chars");
  }
  const secret = randomBytes(SECRET_BYTES).toString("base64url");
  const bearer = `${PREFIX}${secret}`;
  const hash = hashSessionBearer(bearer, pepper);
  return { bearer, hash };
}

export function hashSessionBearer(bearer: string, pepper: string): string {
  if (!bearer.startsWith(PREFIX)) {
    throw new Error("session bearer must start with vtbs_");
  }
  const secret = bearer.slice(PREFIX.length);
  if (secret.length !== ENCODED_SECRET_LEN) {
    throw new Error("session bearer secret length mismatch");
  }
  return createHmac("sha256", pepper).update(secret).digest("hex");
}

export function constantTimeBearerMatch(a: string, b: string): boolean {
  if (a.length !== b.length) {
    return false;
  }
  return timingSafeEqual(Buffer.from(a, "utf8"), Buffer.from(b, "utf8"));
}
