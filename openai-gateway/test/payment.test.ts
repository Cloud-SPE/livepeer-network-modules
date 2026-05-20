import test from 'node:test';
import assert from 'node:assert/strict';
import { tmpdir } from 'node:os';
import path from 'node:path';
import fs from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

import * as grpc from '@grpc/grpc-js';
import * as protoLoader from '@grpc/proto-loader';

import * as payment from '../src/livepeer/payment.js';
import * as paymentErrors from '../src/livepeer/errors.js';
import { createRecorder } from '../src/runtime/recorder.js';

interface CapturedCreatePayment {
  recipient: Buffer;
  ticketParamsBaseUrl?: string;
  acceptedPrice: {
    pricePerUnitWei: { value: Buffer };
    unitsPerPrice: string;
    workUnitName: string;
    capability: string;
    offering: string;
    quoteRef: {
      quoteId: string;
      quoteVersion: string;
      constraintFingerprint: Buffer;
      routeFingerprint: Buffer;
    };
  };
  funding: {
    estimatedUnits: string;
    fundedValueWei: { value: Buffer };
    maxTotalUnits: string;
    topUpAllowed: boolean;
  };
}

interface CapturedReportPaymentResult {
  workId: string;
  capability: string;
  offering: string;
  rejectionReason: string;
}

test('payment.buildPayment sends accepted_price + funding + recipient + ticket_params_base_url and reads payment_bytes back', async (t) => {
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
        workId: 'wid-1',
      });
    },
    reportPaymentResult: (_call: unknown, cb: grpc.sendUnaryData<unknown>) => cb(null, {}),
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
  const minted = await payment.buildPayment({
    capabilityId: 'openai:chat-completions',
    offeringId: 'model-small',
    recipientHex: '0x1111111111111111111111111111111111111111',
    brokerUrl: 'https://broker-a.example.com',
    pricePerUnitWei: '42',
    workUnit: 'token',
    routeFingerprintSource: {
      brokerUrl: 'https://broker-a.example.com',
      offering: 'model-small',
    },
    constraintFingerprintSource: { tier: 'standard' },
  });

  assert.equal(minted.paymentBlob, Buffer.from('test-payment-bytes').toString('base64'));
  assert.equal(minted.workId, 'wid-1');
  assert.equal(captured.length, 1);
  const req = captured[0]!;
  assert.equal(req.ticketParamsBaseUrl, 'https://broker-a.example.com');
  assert.ok(Buffer.isBuffer(req.recipient), 'recipient should be raw bytes (20-byte address)');
  assert.equal(req.recipient.length, 20);
  assert.equal(req.recipient.toString('hex'), '1111111111111111111111111111111111111111');
  assert.equal(req.acceptedPrice.capability, 'openai:chat-completions');
  assert.equal(req.acceptedPrice.offering, 'model-small');
  assert.equal(req.acceptedPrice.workUnitName, 'token');
  assert.equal(req.acceptedPrice.unitsPerPrice, '1');
  assert.equal(bigEndianToBigInt(req.acceptedPrice.pricePerUnitWei.value), 42n);
  assert.match(req.acceptedPrice.quoteRef.quoteId, /^route:[0-9a-f]+$/);
  assert.equal(req.acceptedPrice.quoteRef.quoteVersion, '1');
  assert.equal(req.acceptedPrice.quoteRef.constraintFingerprint.length, 32);
  assert.equal(req.acceptedPrice.quoteRef.routeFingerprint.length, 32);
  assert.equal(req.funding.estimatedUnits, '1');
  assert.equal(req.funding.maxTotalUnits, '1');
  assert.equal(req.funding.topUpAllowed, false);
  assert.equal(bigEndianToBigInt(req.funding.fundedValueWei.value), 1000n);
});

test('payment.buildPayment rejects malformed recipient addresses', async (t) => {
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
    createPayment: (_call: unknown, cb: grpc.sendUnaryData<unknown>) => {
      cb(null, {
        paymentBytes: Buffer.from('test-payment-bytes'),
        ticketsCreated: 1,
        expectedValue: Buffer.from([0x03, 0xe8]),
        workId: 'wid-1',
      });
    },
    reportPaymentResult: (_call: unknown, cb: grpc.sendUnaryData<unknown>) => cb(null, {}),
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
  await assert.rejects(
    () =>
      payment.buildPayment({
        capabilityId: 'openai:chat-completions',
        offeringId: 'model-small',
        recipientHex: 'not-an-address',
        pricePerUnitWei: '42',
        workUnit: 'token',
      }),
    /invalid recipient hex address/,
  );
});

test('payment.withReportedRotationRetry reports INVALID_RECIPIENT_RAND and retries once', async (t) => {
  const capturedCreates: CapturedCreatePayment[] = [];
  const capturedReports: CapturedReportPaymentResult[] = [];

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
      capturedCreates.push(call.request);
      cb(null, {
        paymentBytes: Buffer.from(`test-payment-${capturedCreates.length}`),
        ticketsCreated: 1,
        expectedValue: Buffer.from([0x03, 0xe8]),
        workId: `wid-${capturedCreates.length}`,
      });
    },
    reportPaymentResult: (call: { request: CapturedReportPaymentResult }, cb: grpc.sendUnaryData<unknown>) => {
      capturedReports.push(call.request);
      cb({
        name: 'Error',
        message: 'rotated',
        code: grpc.status.ABORTED,
        details: 'rotated',
      } as grpc.ServiceError);
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
  let attempts = 0;
  const out = await payment.withReportedRotationRetry(
    {
      capabilityId: 'openai:chat-completions',
      offeringId: 'model-small',
      recipientHex: '0x1111111111111111111111111111111111111111',
      brokerUrl: 'https://broker-a.example.com',
      pricePerUnitWei: '42',
      workUnit: 'token',
      routeFingerprintSource: {
        brokerUrl: 'https://broker-a.example.com',
        offering: 'model-small',
      },
      constraintFingerprintSource: { tier: 'standard' },
    },
    async (paymentBlob) => {
      attempts++;
      if (attempts === 1) {
        throw new paymentErrors.LivepeerBrokerError({
          status: 401,
          code: 'payment_invalid',
          message: 'process payment: INVALID_RECIPIENT_RAND',
          responseBody: '{"message":"process payment: INVALID_RECIPIENT_RAND"}',
        });
      }
      return paymentBlob;
    },
  );

  assert.equal(attempts, 2);
  assert.equal(out, Buffer.from('test-payment-2').toString('base64'));
  assert.equal(capturedCreates.length, 2);
  assert.equal(capturedReports.length, 1);
  assert.equal(capturedReports[0]?.workId, 'wid-1');
  assert.equal(capturedReports[0]?.rejectionReason, 'PAYMENT_REJECTION_REASON_INVALID_RECIPIENT_RAND');
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

function bigEndianToBigInt(raw: Buffer): bigint {
  if (raw.length === 0) return 0n;
  return BigInt(`0x${raw.toString('hex')}`);
}
