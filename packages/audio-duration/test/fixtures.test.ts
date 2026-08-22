import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, existsSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

import { estimateCeilingSeconds, probeDuration, ESTIMATOR } from '../src/index.js';
import { estimateCeilingSecondsFromMultipart } from '../src/index.js';

// Walk up to the repo root rather than counting directories: this file
// runs both from source and from the compiled dist-test tree, which sit
// at different depths.
const FIXTURES = (() => {
  const rel = 'livepeer-network-protocol/extractors/fixtures/multipart-audio-duration-v1';
  let dir = dirname(fileURLToPath(import.meta.url));
  for (let i = 0; i < 8; i++) {
    const candidate = join(dir, rel);
    if (existsSync(join(candidate, 'manifest.json'))) return candidate;
    dir = dirname(dir);
  }
  throw new Error(`shared fixtures not found above ${dirname(fileURLToPath(import.meta.url))}`);
})();

interface Fixture {
  file: string;
  format?: string;
  seconds?: number;
  ceiling_seconds?: number;
  reject: boolean;
  why: string;
}

const manifest = JSON.parse(readFileSync(join(FIXTURES, 'manifest.json'), 'utf8')) as {
  estimator: string;
  rounding: string;
  fixtures: Fixture[];
};

test('manifest describes the estimator this package implements', () => {
  assert.equal(manifest.estimator, ESTIMATOR);
  assert.ok(manifest.fixtures.length > 0, 'manifest declares no fixtures');
});

// The shared fixtures are the contract between this implementation and
// the broker's. A client computes a funding ceiling before the work runs
// and the broker bills from its own parse afterwards; if the two
// disagree, the settlement exceeds the ceiling and a correct exchange is
// refused. Running the SAME files on both sides is what makes that a
// test failure here rather than an incident in production.
for (const f of manifest.fixtures) {
  test(`fixture ${f.file}: ${f.why}`, () => {
    const bytes = new Uint8Array(readFileSync(join(FIXTURES, f.file)));

    if (f.reject) {
      assert.throws(
        () => estimateCeilingSeconds(bytes),
        `accepted a fixture that must be refused: ${f.why}`,
      );
      return;
    }

    const ceiling = estimateCeilingSeconds(bytes);
    assert.equal(ceiling, f.ceiling_seconds, 'ceiling disagrees with the shared manifest');

    const res = probeDuration(bytes);
    assert.equal(res.format, f.format);
    assert.ok(res.exact, 'a funding ceiling cannot be built on an estimate');
    assert.ok(
      Math.abs(res.seconds - (f.seconds ?? 0)) < 1e-6,
      `seconds ${res.seconds} != ${f.seconds}`,
    );
  });
}

test('a multipart upload resolves to its file part', () => {
  const audio = new Uint8Array(readFileSync(join(FIXTURES, 'wav-16k-mono-3s.wav')));
  const boundary = 'testboundary';
  const enc = { encode: (t: string) => Uint8Array.from(t, (c) => c.charCodeAt(0) & 0xff) };
  const head = enc.encode(
    `--${boundary}\r\nContent-Disposition: form-data; name="model"\r\n\r\nwhisper-1\r\n` +
      `--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="a.wav"\r\n` +
      `Content-Type: audio/wav\r\n\r\n`,
  );
  const tail = enc.encode(`\r\n--${boundary}--\r\n`);
  const body = new Uint8Array(head.length + audio.length + tail.length);
  body.set(head, 0);
  body.set(audio, head.length);
  body.set(tail, head.length + audio.length);

  const ceiling = estimateCeilingSecondsFromMultipart(
    body,
    `multipart/form-data; boundary=${boundary}`,
  );
  assert.equal(ceiling, 3);
});

test('a raw body is treated as the audio itself', () => {
  const audio = new Uint8Array(readFileSync(join(FIXTURES, 'wav-16k-mono-3s.wav')));
  assert.equal(estimateCeilingSecondsFromMultipart(audio, 'audio/wav'), 3);
});

test('a missing file part is refused, not defaulted', () => {
  const enc = { encode: (t: string) => Uint8Array.from(t, (c) => c.charCodeAt(0) & 0xff) };
  const body = enc.encode(
    `--b\r\nContent-Disposition: form-data; name="model"\r\n\r\nwhisper-1\r\n--b--\r\n`,
  );
  assert.throws(() => estimateCeilingSecondsFromMultipart(body, 'multipart/form-data; boundary=b'));
});
