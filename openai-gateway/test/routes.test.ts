import test from 'node:test';
import assert from 'node:assert/strict';
import { tmpdir } from 'node:os';
import path from 'node:path';
import fs from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

import Fastify, { type FastifyInstance } from 'fastify';
import * as grpc from '@grpc/grpc-js';
import * as protoLoader from '@grpc/proto-loader';

import * as payment from '../src/livepeer/payment.js';
import { buildServer } from '../src/server.js';
import type { Config } from '../src/config.js';
import { HEADER } from '../src/livepeer/headers.js';

interface CapturedBrokerCall {
  capability: string;
  offering: string;
  paymentBlob: string;
  mode: string;
  requestId: string | null;
  contentType: string | null;
  body: Buffer;
}

async function dirExists(p: string): Promise<boolean> {
  try {
    return (await fs.stat(p)).isDirectory();
  } catch {
    return false;
  }
}

async function locateProtoRoot(): Promise<string | null> {
  let dir = path.dirname(fileURLToPath(import.meta.url));
  for (let i = 0; i < 10; i++) {
    const candidate = path.join(dir, 'livepeer-network-protocol', 'proto');
    if (await dirExists(candidate)) return candidate;
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  return null;
}

async function startStubBroker(captures: CapturedBrokerCall[]): Promise<{
  app: FastifyInstance;
  url: string;
}> {
  const app = Fastify({ logger: false, bodyLimit: 100 * 1024 * 1024 });
  app.addContentTypeParser(
    /^multipart\/form-data/,
    { parseAs: 'buffer' },
    (_req, body, done) => done(null, body),
  );
  app.addContentTypeParser(
    'application/json',
    { parseAs: 'buffer' },
    (_req, body, done) => done(null, body),
  );
  app.post('/v1/cap', async (req, reply) => {
    const cap = req.headers[HEADER.CAPABILITY.toLowerCase()] as string | undefined;
    const off = req.headers[HEADER.OFFERING.toLowerCase()] as string | undefined;
    const pay = req.headers[HEADER.PAYMENT.toLowerCase()] as string | undefined;
    const mode = req.headers[HEADER.MODE.toLowerCase()] as string | undefined;
    const rid = req.headers[HEADER.REQUEST_ID.toLowerCase()] as string | undefined;
    const ct = req.headers['content-type'] as string | undefined;
    captures.push({
      capability: cap ?? '',
      offering: off ?? '',
      paymentBlob: pay ?? '',
      mode: mode ?? '',
      requestId: rid ?? null,
      contentType: ct ?? null,
      body: Buffer.isBuffer(req.body) ? req.body : Buffer.from(req.body as string),
    });
    await reply
      .code(200)
      .header('Content-Type', 'application/json')
      .header(HEADER.WORK_UNITS, '7')
      .header(HEADER.REQUEST_ID, rid ?? 'broker-synth')
      .send({ ok: true, echoed: { cap, off, mode } });
  });
  await app.listen({ host: '127.0.0.1', port: 0 });
  const addr = app.server.address();
  if (!addr || typeof addr === 'string') throw new Error('no listen address');
  return { app, url: `http://127.0.0.1:${addr.port}` };
}

async function startStubPayerDaemon(socketPath: string, protoRoot: string): Promise<grpc.Server> {
  const def = await protoLoader.load(
    ['livepeer/payments/v1/types.proto', 'livepeer/payments/v1/payer_daemon.proto'],
    {
      keepCase: false,
      longs: String,
      enums: String,
      defaults: true,
      oneofs: true,
      includeDirs: [protoRoot],
    },
  );
  const proto = grpc.loadPackageDefinition(def) as unknown as {
    livepeer: { payments: { v1: { PayerDaemon: { service: grpc.ServiceDefinition } } } };
  };

  const server = new grpc.Server();
  server.addService(proto.livepeer.payments.v1.PayerDaemon.service, {
    createPayment: (_call: unknown, cb: grpc.sendUnaryData<unknown>) =>
      cb(null, {
        paymentBytes: Buffer.from('stub-payment-bytes'),
        ticketsCreated: 1,
        expectedValue: Buffer.from([0x03, 0xe8]),
      }),
    getDepositInfo: (_call: unknown, cb: grpc.sendUnaryData<unknown>) => cb(null, {}),
    getSessionDebits: (_call: unknown, cb: grpc.sendUnaryData<unknown>) =>
      cb(null, { totalWorkUnits: 0, debitCount: 0, closed: false }),
    health: (_call: unknown, cb: grpc.sendUnaryData<unknown>) => cb(null, { status: 'ok' }),
  });
  await new Promise<void>((res, rej) => {
    server.bindAsync(`unix:${socketPath}`, grpc.ServerCredentials.createInsecure(), (err) =>
      err ? rej(err) : res(),
    );
  });
  return server;
}

test('routes smoke: chat / embeddings / transcriptions / images forward to broker with canonical capability + Livepeer-Request-Id always emitted', async (t) => {
  const protoRoot = await locateProtoRoot();
  if (!protoRoot) {
    t.diagnostic('skipping: livepeer-network-protocol proto tree not found');
    return;
  }
  const tmpDir = await fs.mkdtemp(path.join(tmpdir(), 'openai-gateway-routes-'));
  const sock = path.join(tmpDir, 'payer.sock');
  const captures: CapturedBrokerCall[] = [];
  const broker = await startStubBroker(captures);
  const grpcSrv = await startStubPayerDaemon(sock, protoRoot);

  await payment.init({ socketPath: sock, protoRoot });

  const cfg: Config = {
    brokerUrl: broker.url,
    resolverSocket: null,
    listenPort: 0,
    defaultOffering: 'default',
    payerDaemonSocket: sock,
    paymentProtoRoot: protoRoot,
    resolverProtoRoot: protoRoot,
    resolverSnapshotTtlMs: 15_000,
    offeringsConfigPath: '/dev/null',
    offerings: { defaults: {} },
    audioSpeechEnabled: false,
    brokerCallTimeoutMs: 30_000,
  };
  const server = buildServer(cfg);
  await server.listen({ host: '127.0.0.1', port: 0 });

  t.after(async () => {
    await server.close();
    await broker.app.close();
    await new Promise<void>((res) => grpcSrv.tryShutdown(() => res()));
    payment.shutdown();
    await fs.rm(tmpDir, { recursive: true, force: true });
  });

  const addr = server.server.address();
  if (!addr || typeof addr === 'string') throw new Error('no listen address');
  const base = `http://127.0.0.1:${addr.port}`;

  const chatResp = await fetch(`${base}/v1/chat/completions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ model: 'model-small', messages: [{ role: 'user', content: 'hi' }] }),
  });
  assert.equal(chatResp.status, 200);
  assert.ok(chatResp.headers.get(HEADER.REQUEST_ID), 'gateway must always emit Livepeer-Request-Id');

  const embResp = await fetch(`${base}/v1/embeddings`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', [HEADER.REQUEST_ID]: 'customer-trace-1' },
    body: JSON.stringify({ model: 'text-embedding-3-small', input: ['hello'] }),
  });
  assert.equal(embResp.status, 200);
  assert.equal(embResp.headers.get(HEADER.REQUEST_ID), 'customer-trace-1');

  const imgResp = await fetch(`${base}/v1/images/generations`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ model: 'sdxl', prompt: 'a cat', size: '1024x1024', quality: 'standard' }),
  });
  assert.equal(imgResp.status, 200);

  const speechResp = await fetch(`${base}/v1/audio/speech`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ model: 'kokoro', input: 'hello' }),
  });
  assert.equal(speechResp.status, 503);
  assert.equal(speechResp.headers.get(HEADER.ERROR), 'mode_unsupported');
  assert.ok(speechResp.headers.get(HEADER.REQUEST_ID));

  const boundary = '----testboundary';
  const multipart = Buffer.concat([
    Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="audio.wav"\r\nContent-Type: audio/wav\r\n\r\n`),
    Buffer.from('FAKEAUDIO'),
    Buffer.from(`\r\n--${boundary}--\r\n`),
  ]);
  const txnResp = await fetch(`${base}/v1/audio/transcriptions`, {
    method: 'POST',
    headers: {
      'Content-Type': `multipart/form-data; boundary=${boundary}`,
      'Livepeer-Model': 'whisper-1',
    },
    body: multipart,
  });
  assert.equal(txnResp.status, 200);
  assert.ok(txnResp.headers.get(HEADER.REQUEST_ID));

  // Broker must have seen 4 forwards (speech does not forward; 503's locally).
  assert.equal(captures.length, 4);

  const chat = captures[0]!;
  assert.equal(chat.capability, 'openai:/v1/chat/completions');
  assert.equal(chat.offering, 'model-small');
  assert.equal(chat.mode, 'http-reqresp@v0');
  assert.ok(chat.requestId, 'broker received a Livepeer-Request-Id');

  const emb = captures[1]!;
  assert.equal(emb.capability, 'openai:/v1/embeddings');
  assert.equal(emb.offering, 'text-embedding-3-small');
  assert.equal(emb.mode, 'http-reqresp@v0');
  assert.equal(emb.requestId, 'customer-trace-1');

  const img = captures[2]!;
  assert.equal(img.capability, 'openai:/v1/images/generations');
  assert.equal(img.offering, 'sdxl');
  assert.equal(img.mode, 'http-reqresp@v0');

  const txn = captures[3]!;
  assert.equal(txn.capability, 'openai:/v1/audio/transcriptions');
  assert.equal(txn.offering, 'whisper-1');
  assert.equal(txn.mode, 'http-multipart@v0');
});
