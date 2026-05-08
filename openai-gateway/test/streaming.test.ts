import test from 'node:test';
import assert from 'node:assert/strict';
import { tmpdir } from 'node:os';
import path from 'node:path';
import fs from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

import Fastify from 'fastify';
import * as grpc from '@grpc/grpc-js';
import * as protoLoader from '@grpc/proto-loader';

import * as payment from '../src/livepeer/payment.js';
import { buildServer } from '../src/server.js';
import type { Config } from '../src/config.js';
import { HEADER } from '../src/livepeer/headers.js';

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

test('streaming chat: chunks pass through unbuffered', async (t) => {
  const protoRoot = await locateProtoRoot();
  if (!protoRoot) {
    t.diagnostic('skipping: proto tree not found');
    return;
  }
  const tmpDir = await fs.mkdtemp(path.join(tmpdir(), 'stream-'));
  const sock = path.join(tmpDir, 'payer.sock');

  const broker = Fastify({ logger: false });
  broker.addContentTypeParser(
    'application/json',
    { parseAs: 'buffer' },
    (_req, body, done) => done(null, body),
  );
  broker.post('/v1/cap', async (req, reply) => {
    const rid = req.headers[HEADER.REQUEST_ID.toLowerCase()] as string | undefined;
    reply.raw.statusCode = 200;
    reply.raw.setHeader('Content-Type', 'text/event-stream');
    reply.raw.setHeader('Cache-Control', 'no-cache');
    reply.raw.setHeader(HEADER.REQUEST_ID, rid ?? 'broker-stream-rid');
    reply.hijack();
    reply.raw.write('data: {"choices":[{"delta":{"content":"hello"}}]}\n\n');
    // Force a tick gap so the test can verify chunk-by-chunk delivery.
    await new Promise((res) => setTimeout(res, 50));
    reply.raw.write('data: {"choices":[{"delta":{"content":" world"}}]}\n\n');
    await new Promise((res) => setTimeout(res, 50));
    reply.raw.write(
      'data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}\n\n',
    );
    reply.raw.write('data: [DONE]\n\n');
    reply.raw.end();
  });
  await broker.listen({ host: '127.0.0.1', port: 0 });
  const brokerAddr = broker.server.address();
  if (!brokerAddr || typeof brokerAddr === 'string') throw new Error('no broker addr');

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
  const grpcSrv = new grpc.Server();
  grpcSrv.addService(proto.livepeer.payments.v1.PayerDaemon.service, {
    createPayment: (_call: unknown, cb: grpc.sendUnaryData<unknown>) =>
      cb(null, {
        paymentBytes: Buffer.from('p'),
        ticketsCreated: 1,
        expectedValue: Buffer.from([1]),
      }),
    getDepositInfo: (_call: unknown, cb: grpc.sendUnaryData<unknown>) => cb(null, {}),
    getSessionDebits: (_call: unknown, cb: grpc.sendUnaryData<unknown>) =>
      cb(null, { totalWorkUnits: 0, debitCount: 0, closed: false }),
    health: (_call: unknown, cb: grpc.sendUnaryData<unknown>) => cb(null, { status: 'ok' }),
  });
  await new Promise<void>((res, rej) =>
    grpcSrv.bindAsync(`unix:${sock}`, grpc.ServerCredentials.createInsecure(), (err) =>
      err ? rej(err) : res(),
    ),
  );

  await payment.init({ socketPath: sock, protoRoot });

  const cfg: Config = {
    brokerUrl: `http://127.0.0.1:${brokerAddr.port}`,
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
    await broker.close();
    await new Promise<void>((res) => grpcSrv.tryShutdown(() => res()));
    payment.shutdown();
    await fs.rm(tmpDir, { recursive: true, force: true });
  });

  const addr = server.server.address();
  if (!addr || typeof addr === 'string') throw new Error('no listen address');
  const startedAt = Date.now();
  const resp = await fetch(`http://127.0.0.1:${addr.port}/v1/chat/completions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ model: 'model-small', stream: true, messages: [{ role: 'user', content: 'hi' }] }),
  });
  assert.equal(resp.status, 200);
  assert.match(resp.headers.get('Content-Type') ?? '', /text\/event-stream/);
  assert.ok(resp.headers.get(HEADER.REQUEST_ID), 'gateway must always emit Livepeer-Request-Id');

  if (!resp.body) throw new Error('no body');
  const reader = resp.body.getReader();
  const arrivals: number[] = [];
  let firstChunkAt: number | null = null;
  const decoder = new TextDecoder();
  let collected = '';
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    if (firstChunkAt === null) firstChunkAt = Date.now() - startedAt;
    arrivals.push(Date.now() - startedAt);
    collected += decoder.decode(value, { stream: true });
  }

  assert.ok(arrivals.length >= 2, `expected at least 2 chunks, got ${arrivals.length}`);
  assert.ok(firstChunkAt !== null);
  // First chunk should arrive before the broker has finished writing all
  // chunks (broker sleeps 100ms total). Buffered would land >=100ms; we
  // give a generous 80ms cap to keep the test stable on slow CI.
  assert.ok(firstChunkAt < 80, `first chunk should arrive promptly, got ${firstChunkAt}ms`);
  assert.ok(collected.includes('hello'));
  assert.ok(collected.includes('world'));
  assert.ok(collected.includes('[DONE]'));
});
