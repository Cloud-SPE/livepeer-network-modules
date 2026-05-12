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
import type { CustomerPortal } from '@livepeer-rewrite/customer-portal';
import type { Wallet } from '@livepeer-rewrite/customer-portal/billing';

interface CapturedBrokerCall {
  capability: string;
  offering: string;
  paymentBlob: string;
  mode: string;
  requestId: string | null;
  contentType: string | null;
  body: Buffer;
}

interface TrackingWallet extends Wallet {
  reserveCalls: Array<{ callerId: string; quote: { workId: string; cents: bigint; estimatedTokens: number; model: string; capability: string; callerTier: string } }>;
  commitCalls: Array<{ handle: unknown; usage: { cents: bigint; actualTokens: number; model: string; capability: string } }>;
  refundCalls: unknown[];
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

async function waitFor(
  predicate: () => boolean,
  timeoutMs: number = 250,
): Promise<void> {
  const started = Date.now();
  while (Date.now() - started < timeoutMs) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
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
    if (cap === 'openai:audio-speech') {
      await reply
        .code(200)
        .header('Content-Type', 'audio/mpeg')
        .header(HEADER.WORK_UNITS, '5')
        .header(HEADER.REQUEST_ID, rid ?? 'broker-synth')
        .send(Buffer.from('FAKEAUDIO', 'utf8'));
      return;
    }
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

function createTrackingWallet(): TrackingWallet {
  const reserveCalls: TrackingWallet['reserveCalls'] = [];
  const commitCalls: TrackingWallet['commitCalls'] = [];
  const refundCalls: unknown[] = [];
  return {
    reserveCalls,
    commitCalls,
    refundCalls,
    async reserve(callerId, quote) {
      reserveCalls.push({ callerId, quote: quote as TrackingWallet['reserveCalls'][number]['quote'] });
      return { reservationId: `res-${reserveCalls.length}` };
    },
    async commit(handle, usage) {
      commitCalls.push({ handle, usage: usage as TrackingWallet['commitCalls'][number]['usage'] });
    },
    async refund(handle) {
      refundCalls.push(handle);
    },
  };
}

function createMockRateCardStore(): {
  query: (sql: string) => Promise<{ rows: Record<string, unknown>[] }>;
} {
  return {
    async query(sql: string): Promise<{ rows: Record<string, unknown>[] }> {
      if (sql.startsWith('SELECT tier')) {
        return {
          rows: [
            {
              tier: 'starter',
              input_usd_per_million: '1',
              output_usd_per_million: '2',
            },
          ],
        };
      }
      if (sql.startsWith('SELECT model_or_pattern, is_pattern, tier, sort_order FROM app.rate_card_chat_models')) {
        return {
          rows: [
            {
              model_or_pattern: 'model-small',
              is_pattern: false,
              tier: 'starter',
              sort_order: 1,
            },
          ],
        };
      }
      if (sql.startsWith('SELECT model_or_pattern, is_pattern, usd_per_million_tokens::text, sort_order FROM app.rate_card_embeddings')) {
        return {
          rows: [
            {
              model_or_pattern: 'embed-small',
              is_pattern: false,
              usd_per_million_tokens: '0.25',
              sort_order: 1,
            },
          ],
        };
      }
      if (sql.startsWith('SELECT model_or_pattern, is_pattern, usd_per_million_chars::text, sort_order FROM app.rate_card_audio_speech')) {
        return {
          rows: [
            {
              model_or_pattern: 'kokoro',
              is_pattern: false,
              usd_per_million_chars: '15',
              sort_order: 1,
            },
          ],
        };
      }
      if (sql.startsWith('SELECT model_or_pattern, is_pattern, usd_per_minute::text, sort_order FROM app.rate_card_audio_transcripts')) {
        return {
          rows: [
            {
              model_or_pattern: 'whisper-1',
              is_pattern: false,
              usd_per_minute: '0.6',
              sort_order: 1,
            },
          ],
        };
      }
      if (sql.startsWith('SELECT model_or_pattern, is_pattern, size, quality, usd_per_image::text, sort_order FROM app.rate_card_images')) {
        return {
          rows: [
            {
              model_or_pattern: 'gpt-image-1',
              is_pattern: false,
              size: '1024x1024',
              quality: 'standard',
              usd_per_image: '0.04',
              sort_order: 1,
            },
          ],
        };
      }
      throw new Error(`unexpected rate-card query: ${sql}`);
    },
  };
}

function createPortalForApi(wallet: Wallet): CustomerPortal {
  return {
    authService: {} as CustomerPortal['authService'],
    authResolver: {
      async resolve(req) {
        return req.headers.authorization === 'Bearer sk-live-good'
          ? { id: 'cust-api', tier: 'prepaid', rateLimitTier: 'default' }
          : null;
      },
    },
    customerTokenService: {} as CustomerPortal['customerTokenService'],
    uiAuthResolver: {} as CustomerPortal['uiAuthResolver'],
    issueApiKey: async () => ({ apiKeyId: 'k1', plaintext: 'sk-live-good' }),
    revokeApiKey: async () => undefined,
    wallet,
    webhookEventStore: {} as CustomerPortal['webhookEventStore'],
    adminEngine: {} as CustomerPortal['adminEngine'],
  };
}

test('routes smoke: chat / embeddings / speech / transcriptions / images forward to broker with canonical capability + Livepeer-Request-Id always emitted', async (t) => {
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
    recipientHex: '0x1111111111111111111111111111111111111111',
    listenPort: 0,
    databaseUrl: 'postgres://test:test@localhost:5432/test',
    authPepper: 'test-pepper',
    adminTokens: [],
    publicBaseUrl: null,
    stripe: null,
    defaultOffering: 'default',
    payerDaemonSocket: sock,
    paymentProtoRoot: protoRoot,
    resolverProtoRoot: protoRoot,
    resolverSnapshotTtlMs: 15_000,
    offeringsConfigPath: '/dev/null',
    offerings: { defaults: {} },
    audioSpeechEnabled: true,
    brokerCallTimeoutMs: 30_000,
  };
  const server = await buildServer({ cfg });
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

  const streamResp = await fetch(`${base}/v1/chat/completions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ model: 'model-small', messages: [{ role: 'user', content: 'stream hi' }], stream: true }),
  });
  assert.equal(streamResp.status, 200);
  assert.ok(streamResp.headers.get(HEADER.REQUEST_ID), 'streaming chat must emit Livepeer-Request-Id');

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
  assert.equal(speechResp.status, 200);
  assert.ok(speechResp.headers.get(HEADER.REQUEST_ID));
  assert.equal(speechResp.headers.get('content-type'), 'audio/mpeg');
  assert.equal(Buffer.from(await speechResp.arrayBuffer()).toString('utf8'), 'FAKEAUDIO');

  const boundary = '----testboundary';
  const multipart = Buffer.concat([
    Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="audio.wav"\r\nContent-Type: audio/wav\r\n\r\n`),
    Buffer.from('FAKEAUDIO'),
    Buffer.from(`\r\n--${boundary}\r\nContent-Disposition: form-data; name="model"\r\n\r\nwhisper-1`),
    Buffer.from(`\r\n--${boundary}--\r\n`),
  ]);
  const txnResp = await fetch(`${base}/v1/audio/transcriptions`, {
    method: 'POST',
    headers: {
      'Content-Type': `multipart/form-data; boundary=${boundary}`,
    },
    body: multipart,
  });
  assert.equal(txnResp.status, 200);
  assert.ok(txnResp.headers.get(HEADER.REQUEST_ID));

  assert.equal(captures.length, 6);

  const chat = captures[0]!;
  assert.equal(chat.capability, 'openai:chat-completions');
  assert.equal(chat.offering, 'model-small');
  assert.equal(chat.mode, 'http-reqresp@v0');
  assert.ok(chat.requestId, 'broker received a Livepeer-Request-Id');
  assert.equal(JSON.parse(chat.body.toString('utf8')).stream ?? false, false);

  const streamChat = captures[1]!;
  assert.equal(streamChat.capability, 'openai:chat-completions');
  assert.equal(streamChat.offering, 'model-small');
  assert.equal(streamChat.mode, 'http-stream@v0');
  assert.equal(JSON.parse(streamChat.body.toString('utf8')).stream_options.include_usage, true);

  const emb = captures[2]!;
  assert.equal(emb.capability, 'openai:embeddings');
  assert.equal(emb.offering, 'text-embedding-3-small');
  assert.equal(emb.mode, 'http-reqresp@v0');
  assert.equal(emb.requestId, 'customer-trace-1');

  const img = captures[3]!;
  assert.equal(img.capability, 'openai:images-generations');
  assert.equal(img.offering, 'sdxl');
  assert.equal(img.mode, 'http-reqresp@v0');

  const speech = captures[4]!;
  assert.equal(speech.capability, 'openai:audio-speech');
  assert.equal(speech.offering, 'kokoro');
  assert.equal(speech.mode, 'http-reqresp@v0');
  assert.equal(JSON.parse(speech.body.toString('utf8'))._livepeer_input_chars, 5);

  const txn = captures[5]!;
  assert.equal(txn.capability, 'openai:audio-transcriptions');
  assert.equal(txn.offering, 'whisper-1');
  assert.equal(txn.mode, 'http-multipart@v0');
});

test('authenticated chat reserves then commits usage for API-key req/resp and stream flows', async (t) => {
  const protoRoot = await locateProtoRoot();
  if (!protoRoot) {
    t.diagnostic('skipping: livepeer-network-protocol proto tree not found');
    return;
  }
  const tmpDir = await fs.mkdtemp(path.join(tmpdir(), 'openai-gateway-billing-'));
  const sock = path.join(tmpDir, 'payer.sock');
  const captures: CapturedBrokerCall[] = [];
  const broker = Fastify({ logger: false });
  broker.addContentTypeParser('application/json', { parseAs: 'buffer' }, (_req, body, done) =>
    done(null, body),
  );
  broker.post('/v1/cap', async (req, reply) => {
    const rid = req.headers[HEADER.REQUEST_ID.toLowerCase()] as string | undefined;
    const mode = req.headers[HEADER.MODE.toLowerCase()] as string | undefined;
    captures.push({
      capability: String(req.headers[HEADER.CAPABILITY.toLowerCase()] ?? ''),
      offering: String(req.headers[HEADER.OFFERING.toLowerCase()] ?? ''),
      paymentBlob: String(req.headers[HEADER.PAYMENT.toLowerCase()] ?? ''),
      mode: mode ?? '',
      requestId: rid ?? null,
      contentType: (req.headers['content-type'] as string | undefined) ?? null,
      body: Buffer.isBuffer(req.body) ? req.body : Buffer.from(req.body as string),
    });

    if (mode === 'http-stream@v0') {
      reply.raw.statusCode = 200;
      reply.raw.setHeader('Content-Type', 'text/event-stream');
      reply.raw.setHeader(HEADER.REQUEST_ID, rid ?? 'stream-rid');
      reply.hijack();
      reply.raw.write('data: {"choices":[{"delta":{"content":"hello"}}]}\n\n');
      reply.raw.write('data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}\n\n');
      reply.raw.write('data: [DONE]\n\n');
      reply.raw.end();
      return;
    }

    await reply
      .code(200)
      .header('Content-Type', 'application/json')
      .header(HEADER.WORK_UNITS, '13')
      .header(HEADER.REQUEST_ID, rid ?? 'reqresp-rid')
      .send({
        id: 'chatcmpl-test',
        object: 'chat.completion',
        choices: [{ index: 0, message: { role: 'assistant', content: 'hello' }, finish_reason: 'stop' }],
        usage: { prompt_tokens: 10, completion_tokens: 3, total_tokens: 13 },
      });
  });
  await broker.listen({ host: '127.0.0.1', port: 0 });
  const brokerAddr = broker.server.address();
  if (!brokerAddr || typeof brokerAddr === 'string') throw new Error('no broker addr');

  const grpcSrv = await startStubPayerDaemon(sock, protoRoot);
  await payment.init({ socketPath: sock, protoRoot });

  const wallet = createTrackingWallet();
  const cfg: Config = {
    brokerUrl: `http://127.0.0.1:${brokerAddr.port}`,
    resolverSocket: null,
    recipientHex: '0x1111111111111111111111111111111111111111',
    listenPort: 0,
    databaseUrl: 'postgres://test:test@localhost:5432/test',
    authPepper: 'test-pepper',
    adminTokens: [],
    publicBaseUrl: null,
    stripe: null,
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
  const server = await buildServer({
    cfg,
    portal: createPortalForApi(wallet),
    rateCardStore: createMockRateCardStore() as any,
  });
  await server.listen({ host: '127.0.0.1', port: 0 });

  t.after(async () => {
    await server.close();
    await broker.close();
    await new Promise<void>((res) => grpcSrv.tryShutdown(() => res()));
    payment.shutdown();
    await fs.rm(tmpDir, { recursive: true, force: true });
  });

  const addr = server.server.address();
  if (!addr || typeof addr === 'string') throw new Error('no listen address');
  const base = `http://127.0.0.1:${addr.port}`;

  const reqresp = await fetch(`${base}/v1/chat/completions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', authorization: 'Bearer sk-live-good' },
    body: JSON.stringify({ model: 'model-small', max_tokens: 50, messages: [{ role: 'user', content: 'hi' }] }),
  });
  assert.equal(reqresp.status, 200);

  const stream = await fetch(`${base}/v1/chat/completions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', authorization: 'Bearer sk-live-good' },
    body: JSON.stringify({ model: 'model-small', max_tokens: 50, stream: true, messages: [{ role: 'user', content: 'hi' }] }),
  });
  assert.equal(stream.status, 200);
  await stream.text();
  await waitFor(() => wallet.commitCalls.length === 2);

  assert.equal(wallet.reserveCalls.length, 2);
  assert.equal(wallet.commitCalls.length, 2);
  assert.equal(wallet.refundCalls.length, 0);
  assert.deepEqual(
    wallet.commitCalls.map((call) => call.usage.actualTokens),
    [13, 13],
  );
  assert.ok(
    wallet.commitCalls.every((call) => call.usage.capability === 'openai:chat-completions'),
  );
  assert.ok(
    wallet.commitCalls.every((call) => call.usage.model === 'model-small'),
  );
  assert.equal(captures[0]?.mode, 'http-reqresp@v0');
  assert.equal(captures[1]?.mode, 'http-stream@v0');
});

test('openai api routes reject UI auth tokens with 401', async (t) => {
  const protoRoot = await locateProtoRoot();
  if (!protoRoot) {
    t.diagnostic('skipping: livepeer-network-protocol proto tree not found');
    return;
  }
  const tmpDir = await fs.mkdtemp(path.join(tmpdir(), 'openai-gateway-ui-auth-reject-'));
  const sock = path.join(tmpDir, 'payer.sock');
  const broker = Fastify({ logger: false });
  await broker.listen({ host: '127.0.0.1', port: 0 });
  const brokerAddr = broker.server.address();
  if (!brokerAddr || typeof brokerAddr === 'string') throw new Error('no broker addr');

  const grpcSrv = await startStubPayerDaemon(sock, protoRoot);
  await payment.init({ socketPath: sock, protoRoot });

  const wallet = createTrackingWallet();
  const cfg: Config = {
    brokerUrl: `http://127.0.0.1:${brokerAddr.port}`,
    resolverSocket: null,
    recipientHex: '0x1111111111111111111111111111111111111111',
    listenPort: 0,
    databaseUrl: 'postgres://test:test@localhost:5432/test',
    authPepper: 'test-pepper',
    adminTokens: [],
    publicBaseUrl: null,
    stripe: null,
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
  const server = await buildServer({
    cfg,
    portal: createPortalForApi(wallet),
    rateCardStore: createMockRateCardStore() as any,
  });
  await server.listen({ host: '127.0.0.1', port: 0 });

  t.after(async () => {
    await server.close();
    await broker.close();
    await new Promise<void>((res) => grpcSrv.tryShutdown(() => res()));
    payment.shutdown();
    await fs.rm(tmpDir, { recursive: true, force: true });
  });

  const addr = server.server.address();
  if (!addr || typeof addr === 'string') throw new Error('no listen address');
  const base = `http://127.0.0.1:${addr.port}`;

  const resp = await fetch(`${base}/v1/chat/completions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', authorization: 'Bearer ui-good' },
    body: JSON.stringify({ model: 'model-small', messages: [{ role: 'user', content: 'hi' }] }),
  });

  assert.equal(resp.status, 401);
  assert.equal(wallet.reserveCalls.length, 0);
  assert.equal(wallet.commitCalls.length, 0);
  assert.equal(wallet.refundCalls.length, 0);
});

test('authenticated embeddings, images, speech, and transcriptions reserve then commit usage', async (t) => {
  const protoRoot = await locateProtoRoot();
  if (!protoRoot) {
    t.diagnostic('skipping: livepeer-network-protocol proto tree not found');
    return;
  }
  const tmpDir = await fs.mkdtemp(path.join(tmpdir(), 'openai-gateway-non-chat-billing-'));
  const sock = path.join(tmpDir, 'payer.sock');
  const captures: CapturedBrokerCall[] = [];
  const broker = Fastify({ logger: false });
  broker.addContentTypeParser('application/json', { parseAs: 'buffer' }, (_req, body, done) =>
    done(null, body),
  );
  broker.addContentTypeParser(/^multipart\/form-data/, { parseAs: 'buffer' }, (_req, body, done) =>
    done(null, body),
  );
  broker.post('/v1/cap', async (req, reply) => {
    const rid = req.headers[HEADER.REQUEST_ID.toLowerCase()] as string | undefined;
    const capability = String(req.headers[HEADER.CAPABILITY.toLowerCase()] ?? '');
    captures.push({
      capability,
      offering: String(req.headers[HEADER.OFFERING.toLowerCase()] ?? ''),
      paymentBlob: String(req.headers[HEADER.PAYMENT.toLowerCase()] ?? ''),
      mode: String(req.headers[HEADER.MODE.toLowerCase()] ?? ''),
      requestId: rid ?? null,
      contentType: (req.headers['content-type'] as string | undefined) ?? null,
      body: Buffer.isBuffer(req.body) ? req.body : Buffer.from(req.body as string),
    });

    if (capability === 'openai:embeddings') {
      await reply
        .code(200)
        .header('Content-Type', 'application/json')
        .header(HEADER.WORK_UNITS, '10')
        .header(HEADER.REQUEST_ID, rid ?? 'emb-rid')
        .send({
          object: 'list',
          data: [{ object: 'embedding', index: 0, embedding: [0, 0] }],
          model: 'embed-small',
          usage: { prompt_tokens: 10, total_tokens: 10 },
        });
      return;
    }

    if (capability === 'openai:images-generations') {
      await reply
        .code(200)
        .header('Content-Type', 'application/json')
        .header(HEADER.WORK_UNITS, '2')
        .header(HEADER.REQUEST_ID, rid ?? 'img-rid')
        .send({
          created: 1,
          data: [{ b64_json: 'a' }, { b64_json: 'b' }],
        });
      return;
    }

    if (capability === 'openai:audio-transcriptions') {
      await reply
        .code(200)
        .header('Content-Type', 'application/json')
        .header(HEADER.WORK_UNITS, '42')
        .header(HEADER.REQUEST_ID, rid ?? 'txn-rid')
        .send({ text: 'mock transcript' });
      return;
    }

    if (capability === 'openai:audio-speech') {
      await reply
        .code(200)
        .header('Content-Type', 'audio/mpeg')
        .header(HEADER.WORK_UNITS, '5')
        .header(HEADER.REQUEST_ID, rid ?? 'speech-rid')
        .send(Buffer.from('FAKEAUDIO', 'utf8'));
      return;
    }

    await reply.code(500).send({ error: 'unexpected_capability', capability });
  });
  await broker.listen({ host: '127.0.0.1', port: 0 });
  const brokerAddr = broker.server.address();
  if (!brokerAddr || typeof brokerAddr === 'string') throw new Error('no broker addr');

  const grpcSrv = await startStubPayerDaemon(sock, protoRoot);
  await payment.init({ socketPath: sock, protoRoot });

  const wallet = createTrackingWallet();
  const cfg: Config = {
    brokerUrl: `http://127.0.0.1:${brokerAddr.port}`,
    resolverSocket: null,
    recipientHex: '0x1111111111111111111111111111111111111111',
    listenPort: 0,
    databaseUrl: 'postgres://test:test@localhost:5432/test',
    authPepper: 'test-pepper',
    adminTokens: [],
    publicBaseUrl: null,
    stripe: null,
    defaultOffering: 'default',
    payerDaemonSocket: sock,
    paymentProtoRoot: protoRoot,
    resolverProtoRoot: protoRoot,
    resolverSnapshotTtlMs: 15_000,
    offeringsConfigPath: '/dev/null',
    offerings: { defaults: {} },
    audioSpeechEnabled: true,
    brokerCallTimeoutMs: 30_000,
  };
  const server = await buildServer({
    cfg,
    portal: createPortalForApi(wallet),
    rateCardStore: createMockRateCardStore() as any,
  });
  await server.listen({ host: '127.0.0.1', port: 0 });

  t.after(async () => {
    await server.close();
    await broker.close();
    await new Promise<void>((res) => grpcSrv.tryShutdown(() => res()));
    payment.shutdown();
    await fs.rm(tmpDir, { recursive: true, force: true });
  });

  const addr = server.server.address();
  if (!addr || typeof addr === 'string') throw new Error('no listen address');
  const base = `http://127.0.0.1:${addr.port}`;

  const embResp = await fetch(`${base}/v1/embeddings`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', authorization: 'Bearer sk-live-good' },
    body: JSON.stringify({ model: 'embed-small', input: ['hello', 'world'] }),
  });
  assert.equal(embResp.status, 200);

  const imgResp = await fetch(`${base}/v1/images/generations`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', authorization: 'Bearer sk-live-good' },
    body: JSON.stringify({ model: 'gpt-image-1', prompt: 'draw a cat', n: 2 }),
  });
  assert.equal(imgResp.status, 200);

  const speechResp = await fetch(`${base}/v1/audio/speech`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', authorization: 'Bearer sk-live-good' },
    body: JSON.stringify({ model: 'kokoro', input: 'hello' }),
  });
  assert.equal(speechResp.status, 200);
  assert.equal(Buffer.from(await speechResp.arrayBuffer()).toString('utf8'), 'FAKEAUDIO');

  const boundary = '----codextest';
  const multipart = Buffer.from(
    `--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="sample.wav"\r\nContent-Type: audio/wav\r\n\r\nmockaudio\r\n--${boundary}\r\nContent-Disposition: form-data; name="model"\r\n\r\nwhisper-1\r\n--${boundary}--\r\n`,
    'utf8',
  );
  const txnResp = await fetch(`${base}/v1/audio/transcriptions`, {
    method: 'POST',
    headers: {
      'Content-Type': `multipart/form-data; boundary=${boundary}`,
      authorization: 'Bearer sk-live-good',
    },
    body: multipart,
  });
  assert.equal(txnResp.status, 200);

  assert.equal(wallet.reserveCalls.length, 4);
  assert.equal(wallet.commitCalls.length, 4);
  assert.equal(wallet.refundCalls.length, 0);
  assert.deepEqual(
    wallet.commitCalls.map((call) => ({ capability: call.usage.capability, actualTokens: call.usage.actualTokens, model: call.usage.model })),
    [
      { capability: 'openai:embeddings', actualTokens: 10, model: 'embed-small' },
      { capability: 'openai:images-generations', actualTokens: 2, model: 'gpt-image-1' },
      { capability: 'openai:audio-speech', actualTokens: 5, model: 'kokoro' },
      { capability: 'openai:audio-transcriptions', actualTokens: 42, model: 'whisper-1' },
    ],
  );
  assert.deepEqual(
    captures.map((capture) => ({ capability: capture.capability, mode: capture.mode })),
    [
      { capability: 'openai:embeddings', mode: 'http-reqresp@v0' },
      { capability: 'openai:images-generations', mode: 'http-reqresp@v0' },
      { capability: 'openai:audio-speech', mode: 'http-reqresp@v0' },
      { capability: 'openai:audio-transcriptions', mode: 'http-multipart@v0' },
    ],
  );
  const speechCapture = captures.find((capture) => capture.capability === 'openai:audio-speech');
  assert.ok(speechCapture);
  assert.equal(JSON.parse(speechCapture!.body.toString('utf8'))._livepeer_input_chars, 5);
});

test('authenticated chat refunds reservation when upstream fails before completion', async (t) => {
  const protoRoot = await locateProtoRoot();
  if (!protoRoot) {
    t.diagnostic('skipping: livepeer-network-protocol proto tree not found');
    return;
  }
  const tmpDir = await fs.mkdtemp(path.join(tmpdir(), 'openai-gateway-refund-'));
  const sock = path.join(tmpDir, 'payer.sock');
  const broker = Fastify({ logger: false });
  broker.addContentTypeParser('application/json', { parseAs: 'buffer' }, (_req, body, done) =>
    done(null, body),
  );
  broker.post('/v1/cap', async (_req, reply) => {
    await reply.code(503).header('Content-Type', 'application/json').send({
      error: 'temporarily_unavailable',
      message: 'try again later',
    });
  });
  await broker.listen({ host: '127.0.0.1', port: 0 });
  const brokerAddr = broker.server.address();
  if (!brokerAddr || typeof brokerAddr === 'string') throw new Error('no broker addr');

  const grpcSrv = await startStubPayerDaemon(sock, protoRoot);
  await payment.init({ socketPath: sock, protoRoot });

  const wallet = createTrackingWallet();
  const cfg: Config = {
    brokerUrl: `http://127.0.0.1:${brokerAddr.port}`,
    resolverSocket: null,
    recipientHex: '0x1111111111111111111111111111111111111111',
    listenPort: 0,
    databaseUrl: 'postgres://test:test@localhost:5432/test',
    authPepper: 'test-pepper',
    adminTokens: [],
    publicBaseUrl: null,
    stripe: null,
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
  const server = await buildServer({
    cfg,
    portal: createPortalForApi(wallet),
    rateCardStore: createMockRateCardStore() as any,
  });
  await server.listen({ host: '127.0.0.1', port: 0 });

  t.after(async () => {
    await server.close();
    await broker.close();
    await new Promise<void>((res) => grpcSrv.tryShutdown(() => res()));
    payment.shutdown();
    await fs.rm(tmpDir, { recursive: true, force: true });
  });

  const addr = server.server.address();
  if (!addr || typeof addr === 'string') throw new Error('no listen address');
  const base = `http://127.0.0.1:${addr.port}`;

  const resp = await fetch(`${base}/v1/chat/completions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', authorization: 'Bearer sk-live-good' },
    body: JSON.stringify({ model: 'model-small', messages: [{ role: 'user', content: 'hi' }] }),
  });

  assert.equal(resp.status, 502);
  assert.equal(wallet.reserveCalls.length, 1);
  assert.equal(wallet.commitCalls.length, 0);
  assert.equal(wallet.refundCalls.length, 1);
});
