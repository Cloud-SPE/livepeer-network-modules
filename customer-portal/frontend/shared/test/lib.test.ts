import { test } from 'node:test';
import assert from 'node:assert/strict';
import { isValidEmail, isStrongPassword, validateSignup } from '../src/lib/validators.js';
import { ApiClient } from '../src/lib/api-base.js';

test('isValidEmail accepts valid addresses, rejects malformed', () => {
  assert.equal(isValidEmail('alice@example.com'), true);
  assert.equal(isValidEmail('not-an-email'), false);
  assert.equal(isValidEmail(''), false);
});

test('isStrongPassword requires 8+ chars', () => {
  assert.equal(isStrongPassword('a'.repeat(8)), true);
  assert.equal(isStrongPassword('short'), false);
});

test('validateSignup accumulates errors', () => {
  const r1 = validateSignup({ email: 'alice@example.com', password: 'longenough' });
  assert.equal(r1.ok, true);
  const r2 = validateSignup({ email: 'bad', password: 's' });
  assert.equal(r2.ok, false);
  assert.ok(r2.errors['email']);
  assert.ok(r2.errors['password']);
});

test('ApiClient.post serializes JSON and parses response', async () => {
  let capturedUrl = '';
  let capturedBody = '';
  const fakeFetch = (async (url: string, init?: RequestInit) => {
    capturedUrl = url;
    capturedBody = init?.body as string;
    return new Response(JSON.stringify({ ok: true }), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    });
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any;
  const client = new ApiClient({ baseUrl: 'http://test', fetchImpl: fakeFetch });
  const result = await client.post<{ ok: boolean }>('/foo', { x: 1 });
  assert.equal(capturedUrl, 'http://test/foo');
  assert.equal(capturedBody, JSON.stringify({ x: 1 }));
  assert.deepEqual(result, { ok: true });
});

test('ApiClient throws ApiError on non-2xx', async () => {
  const fakeFetch = (async () =>
    new Response('{"error":"nope"}', {
      status: 500,
      headers: { 'content-type': 'application/json' },
    })) as unknown as typeof fetch;
  const client = new ApiClient({ fetchImpl: fakeFetch });
  await assert.rejects(() => client.get('/x'), /500|nope|.*/);
});
