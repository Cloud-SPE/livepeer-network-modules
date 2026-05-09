import { createHmac, randomBytes, timingSafeEqual } from 'node:crypto';

const TOKEN_RANDOM_BYTES = 32;
export const UI_AUTH_TOKEN_PATTERN = /^tok-(live|test)-[A-Za-z0-9_-]{43}$/;

export function generateUiAuthToken(envPrefix: 'live' | 'test'): string {
  return `tok-${envPrefix}-${randomBytes(TOKEN_RANDOM_BYTES).toString('base64url')}`;
}

export function hashUiAuthToken(pepper: string, plaintext: string): string {
  return createHmac('sha256', pepper).update(plaintext).digest('hex');
}

export function verifyUiAuthToken(expectedHash: string, actualHash: string): boolean {
  if (expectedHash.length !== actualHash.length) return false;
  return timingSafeEqual(Buffer.from(expectedHash, 'hex'), Buffer.from(actualHash, 'hex'));
}
