import { createHmac, randomBytes, timingSafeEqual } from 'node:crypto';

export type EnvPrefix = 'live' | 'test';

const KEY_RANDOM_BYTES = 32;
export const API_KEY_PATTERN = /^sk-(live|test)-[A-Za-z0-9_-]{43}$/;

export function generateApiKey(envPrefix: EnvPrefix): string {
  const random = randomBytes(KEY_RANDOM_BYTES).toString('base64url');
  return `sk-${envPrefix}-${random}`;
}

export function hashApiKey(pepper: string, plaintext: string): string {
  return createHmac('sha256', pepper).update(plaintext).digest('hex');
}

export function verifyApiKey(expectedHash: string, actualHash: string): boolean {
  if (expectedHash.length !== actualHash.length) return false;
  return timingSafeEqual(Buffer.from(expectedHash, 'hex'), Buffer.from(actualHash, 'hex'));
}
