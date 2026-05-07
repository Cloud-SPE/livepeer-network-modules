import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  API_KEY_PATTERN,
  generateApiKey,
  hashApiKey,
  verifyApiKey,
  TtlCache,
} from '../src/auth/index.js';

test('generateApiKey produces sk-{env}-{43} format', () => {
  const live = generateApiKey('live');
  const t = generateApiKey('test');
  assert.match(live, API_KEY_PATTERN);
  assert.match(t, API_KEY_PATTERN);
  assert.ok(live.startsWith('sk-live-'));
  assert.ok(t.startsWith('sk-test-'));
});

test('generated keys are unique across calls', () => {
  const a = generateApiKey('live');
  const b = generateApiKey('live');
  assert.notEqual(a, b);
});

test('hashApiKey is deterministic with same pepper', () => {
  const k = generateApiKey('live');
  const h1 = hashApiKey('pepper-a', k);
  const h2 = hashApiKey('pepper-a', k);
  assert.equal(h1, h2);
});

test('hashApiKey changes with pepper', () => {
  const k = generateApiKey('live');
  assert.notEqual(hashApiKey('pepper-a', k), hashApiKey('pepper-b', k));
});

test('verifyApiKey true for matching hashes, false for mismatch', () => {
  const k = generateApiKey('live');
  const h = hashApiKey('p', k);
  assert.equal(verifyApiKey(h, h), true);
  assert.equal(verifyApiKey(h, h.replace(/.$/, '0')), false);
});

test('verifyApiKey rejects different lengths without timing leak', () => {
  assert.equal(verifyApiKey('abcd', 'abc'), false);
});

test('TtlCache hit/miss within ttl', () => {
  const c = new TtlCache<string, number>(60_000);
  assert.equal(c.get('k'), null);
  c.set('k', 42);
  assert.equal(c.get('k'), 42);
  assert.equal(c.size, 1);
});

test('TtlCache expiry returns null and removes entry', async () => {
  const c = new TtlCache<string, number>(5);
  c.set('k', 1);
  await new Promise((r) => setTimeout(r, 15));
  assert.equal(c.get('k'), null);
  assert.equal(c.size, 0);
});

test('TtlCache delete + clear', () => {
  const c = new TtlCache<string, number>(60_000);
  c.set('a', 1);
  c.set('b', 2);
  c.delete('a');
  assert.equal(c.get('a'), null);
  assert.equal(c.get('b'), 2);
  c.clear();
  assert.equal(c.size, 0);
});

test('API_KEY_PATTERN rejects malformed inputs', () => {
  assert.equal(API_KEY_PATTERN.test('sk-prod-' + 'a'.repeat(43)), false);
  assert.equal(API_KEY_PATTERN.test('sk-live-' + 'a'.repeat(42)), false);
  assert.equal(API_KEY_PATTERN.test('sk-live-' + 'a'.repeat(44)), false);
  assert.equal(API_KEY_PATTERN.test('not-a-key'), false);
});
