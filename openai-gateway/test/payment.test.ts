import test from 'node:test';
import assert from 'node:assert/strict';
import { tmpdir } from 'node:os';
import path from 'node:path';
import fs from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

import * as grpc from '@grpc/grpc-js';
import * as protoLoader from '@grpc/proto-loader';

import * as payment from '../src/livepeer/payment.js';
import { createRecorder } from '../src/runtime/recorder.js';

interface CapturedCreatePayment {
  faceValue: Buffer;
  recipient: Buffer;
  capability: string;
  offering: string;
}

test('payment.buildPayment sends face_value + recipient + capability + offering and reads payment_bytes back', async (t) => {
  const captured: CapturedCreatePayment[] = [];

  const tmpDir = await fs.mkdtemp(path.join(tmpdir(), 'openai-gateway-payment-'));
  const sock = path.join(tmpDir, 'payer.sock');
  const repoProtoRoot = await locateProtoRoot();
  if (!repoProtoRoot) {
    t.diagnostic('skipping: livepeer-network-protocol proto tree not found');
    return;
  }
  const usingProtoRoot = repoProtoRoot;
  const def = await protoLoader.load(
    [
      'livepeer/payments/v1/types.proto',
      'livepeer/payments/v1/payer_daemon.proto',
    ],
    {
      keepCase: false,
      longs: String,
      enums: String,
      defaults: true,
      oneofs: true,
      includeDirs: [usingProtoRoot],
    },
  );
  const proto = grpc.loadPackageDefinition(def) as unknown as {
    livepeer: { payments: { v1: { PayerDaemon: { service: grpc.ServiceDefinition } } } };
  };

  const server = new grpc.Server();
  server.addService(proto.livepeer.payments.v1.PayerDaemon.service, {
    createPayment: (call: { request: CapturedCreatePayment }, cb: grpc.sendUnaryData<unknown>) => {
      captured.push(call.request);
      cb(null, {
        paymentBytes: Buffer.from('test-payment-bytes'),
        ticketsCreated: 1,
        expectedValue: Buffer.from([0x03, 0xe8]),
      });
    },
    getDepositInfo: (_call: unknown, cb: grpc.sendUnaryData<unknown>) => cb(null, {}),
    getSessionDebits: (_call: unknown, cb: grpc.sendUnaryData<unknown>) =>
      cb(null, { totalWorkUnits: 0, debitCount: 0, closed: false }),
    health: (_call: unknown, cb: grpc.sendUnaryData<unknown>) => cb(null, { status: 'ok' }),
  });
  await new Promise<void>((res, rej) => {
    server.bindAsync(`unix:${sock}`, grpc.ServerCredentials.createInsecure(), (err) =>
      err ? rej(err) : res(),
    );
  });

  t.after(async () => {
    await new Promise<void>((res) => server.tryShutdown(() => res()));
    payment.shutdown();
    await fs.rm(tmpDir, { recursive: true, force: true });
  });

  await payment.init({ socketPath: sock, protoRoot: usingProtoRoot });
  const blob = await payment.buildPayment({
    capabilityId: 'openai:chat-completions',
    offeringId: 'model-small',
  });

  assert.equal(blob, Buffer.from('test-payment-bytes').toString('base64'));
  assert.equal(captured.length, 1);
  const req = captured[0]!;
  assert.equal(req.capability, 'openai:chat-completions');
  assert.equal(req.offering, 'model-small');
  assert.ok(Buffer.isBuffer(req.faceValue), 'face_value should be raw bytes (big-endian)');
  assert.ok(Buffer.isBuffer(req.recipient), 'recipient should be raw bytes (20-byte address)');
  assert.equal(req.recipient.length, 20);
  assert.ok(req.faceValue.length > 0, 'face_value should be non-empty for the default 1000 wei');
});

test('Recorder accumulates work-unit records and drains them', () => {
  const r = createRecorder({ now: () => 42, capacity: 4 });
  r.record({
      callerId: 'cust-1',
      capability: 'openai:chat-completions',
      offering: 'model-small',
    workUnits: 10n,
    expectedValueWei: 5_000n,
  });
  r.record({
      callerId: 'cust-1',
      capability: 'openai:embeddings',
    offering: 'text-embedding-3-small',
    workUnits: 1n,
    expectedValueWei: 100n,
  });
  assert.equal(r.size(), 2);
  const drained = r.drain();
  assert.equal(drained.length, 2);
  assert.equal(drained[0]?.recordedAt, 42);
  assert.equal(drained[1]?.workUnits, 1n);
  assert.equal(r.size(), 0);
});

test('Recorder evicts oldest entry past capacity', () => {
  const r = createRecorder({ capacity: 2 });
  for (let i = 0; i < 5; i++) {
    r.record({
      callerId: `c${i}`,
      capability: 'openai:embeddings',
      offering: 'text-embedding-3-small',
      workUnits: BigInt(i),
      expectedValueWei: 0n,
    });
  }
  assert.equal(r.size(), 2);
  const drained = r.drain();
  assert.equal(drained[0]?.callerId, 'c3');
  assert.equal(drained[1]?.callerId, 'c4');
});

async function dirExists(p: string): Promise<boolean> {
  try {
    const stat = await fs.stat(p);
    return stat.isDirectory();
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
