import test from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import os from 'node:os';
import fs from 'node:fs/promises';

import {
  parseOfferingsYaml,
  resolveDefaultOffering,
  loadOfferingsFromDisk,
} from '../src/service/offerings.js';

test('parseOfferingsYaml accepts the documented sample shape', () => {
  const cfg = parseOfferingsYaml(`
defaults:
  "openai:/v1/chat/completions":
    streaming: vllm-h100-stream
    non-streaming: vllm-h100-batch4
  "openai:/v1/embeddings":
    default: bge-large-en
  "openai:/v1/images/generations":
    default: realvis-xl-v4-lightning
`);
  assert.equal(typeof cfg.defaults['openai:/v1/embeddings'], 'object');
});

test('resolveDefaultOffering picks streaming vs non-streaming variants', () => {
  const cfg = parseOfferingsYaml(`
defaults:
  "openai:/v1/chat/completions":
    streaming: a
    non-streaming: b
  "openai:/v1/embeddings":
    default: e
`);
  assert.equal(
    resolveDefaultOffering(cfg, { capability: 'openai:/v1/chat/completions', variant: 'streaming' }),
    'a',
  );
  assert.equal(
    resolveDefaultOffering(cfg, {
      capability: 'openai:/v1/chat/completions',
      variant: 'non-streaming',
    }),
    'b',
  );
  assert.equal(resolveDefaultOffering(cfg, { capability: 'openai:/v1/embeddings' }), 'e');
  assert.equal(resolveDefaultOffering(cfg, { capability: 'openai:/v1/audio/speech' }), null);
});

test('loadOfferingsFromDisk returns empty defaults when file is absent', () => {
  const cfg = loadOfferingsFromDisk('/dev/null/no-such-file');
  assert.deepEqual(cfg, { defaults: {} });
});

test('loadOfferingsFromDisk reads a real file', async () => {
  const tmp = await fs.mkdtemp(path.join(os.tmpdir(), 'offerings-'));
  const file = path.join(tmp, 'offerings.yaml');
  await fs.writeFile(file, 'defaults:\n  "openai:/v1/embeddings":\n    default: e\n');
  try {
    const cfg = loadOfferingsFromDisk(file);
    assert.equal(resolveDefaultOffering(cfg, { capability: 'openai:/v1/embeddings' }), 'e');
  } finally {
    await fs.rm(tmp, { recursive: true, force: true });
  }
});
