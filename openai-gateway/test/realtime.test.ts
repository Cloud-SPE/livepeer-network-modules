import test from 'node:test';
import assert from 'node:assert/strict';
import { tmpdir } from 'node:os';
import path from 'node:path';
import fs from 'node:fs/promises';
import * as http from 'node:http';
import { fileURLToPath } from 'node:url';

import * as grpc from '@grpc/grpc-js';
import * as protoLoader from '@grpc/proto-loader';
import { WebSocket, WebSocketServer, type WebSocket as ServerWebSocket } from 'ws';

import * as payment from '../src/livepeer/payment.js';
import { buildServer } from '../src/server.js';
import type { Config } from '../src/config.js';
import { HEADER, SPEC_VERSION } from '../src/livepeer/headers.js';

interface UpgradeCapture {
  path: string | undefined;
  headers: http.IncomingHttpHeaders;
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

interface StubBroker {
  url: string;
  upgrades: UpgradeCapture[];
  close: () => Promise<void>;
}

async function startStubBroker(): Promise<StubBroker> {
  const upgrades: UpgradeCapture[] = [];
  const httpServer = http.createServer((_req, res) => {
    res.statusCode = 404;
    res.end();
  });
  const wss = new WebSocketServer({ noServer: true });
  wss.on('connection', (ws: ServerWebSocket) => {
    ws.on('message', (data, isBinary) => {
      ws.send(data, { binary: isBinary });
    });
  });
  httpServer.on('upgrade', (req, socket, head) => {
    upgrades.push({ path: req.url, headers: req.headers });
    if (req.url !== '/v1/cap') {
      socket.write('HTTP/1.1 404 Not Found\r\n\r\n');
      socket.destroy();
      return;
    }
    wss.handleUpgrade(req, socket, head, (ws) => {
      wss.emit('connection', ws, req);
    });
  });
  await new Promise<void>((resolve) => httpServer.listen(0, '127.0.0.1', resolve));
  const addr = httpServer.address();
  if (!addr || typeof addr === 'string') throw new Error('no broker addr');
  return {
    url: `http://127.0.0.1:${addr.port}`,
    upgrades,
    close: async () => {
      wss.close();
      await new Promise<void>((res) => httpServer.close(() => res()));
    },
  };
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
        paymentBytes: Buffer.from('stub-realtime-payment'),
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

test('realtime: customer ws bridges to broker via ws-realtime adapter with canonical headers', async (t) => {
  const protoRoot = await locateProtoRoot();
  if (!protoRoot) {
    t.diagnostic('skipping: livepeer-network-protocol proto tree not found');
    return;
  }
  const tmpDir = await fs.mkdtemp(path.join(tmpdir(), 'openai-gateway-realtime-'));
  const sock = path.join(tmpDir, 'payer.sock');
  const broker = await startStubBroker();
  const grpcSrv = await startStubPayerDaemon(sock, protoRoot);

  await payment.init({ socketPath: sock, protoRoot });

  const cfg: Config = {
    brokerUrl: broker.url,
    resolverSocket: null,
    listenPort: 0,
    defaultOffering: 'gpt-4o-realtime',
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

  const customerSocket = new WebSocket(
    `ws://127.0.0.1:${addr.port}/v1/realtime?model=gpt-4o-realtime`,
    {
      headers: { [HEADER.REQUEST_ID]: 'rt-test-1' },
    },
  );
  await new Promise<void>((resolve, reject) => {
    customerSocket.once('open', () => resolve());
    customerSocket.once('error', reject);
  });

  await waitFor(() => broker.upgrades.length > 0, 1000);
  const captured = broker.upgrades[0]!;
  assert.equal(captured.path, '/v1/cap');
  assert.equal(captured.headers[HEADER.CAPABILITY.toLowerCase()], 'openai:/v1/realtime');
  assert.equal(captured.headers[HEADER.OFFERING.toLowerCase()], 'gpt-4o-realtime');
  assert.equal(
    captured.headers[HEADER.PAYMENT.toLowerCase()],
    Buffer.from('stub-realtime-payment').toString('base64'),
  );
  assert.equal(captured.headers[HEADER.SPEC_VERSION.toLowerCase()], SPEC_VERSION);
  assert.equal(captured.headers[HEADER.MODE.toLowerCase()], 'ws-realtime@v0');
  assert.equal(captured.headers[HEADER.REQUEST_ID.toLowerCase()], 'rt-test-1');

  const echoed = await new Promise<string>((resolve, reject) => {
    customerSocket.once('message', (data) => resolve(data.toString()));
    customerSocket.once('error', reject);
    customerSocket.send('hello-realtime');
  });
  assert.equal(echoed, 'hello-realtime');

  customerSocket.close(1000, 'done');
  await new Promise<void>((resolve) => {
    if (customerSocket.readyState === customerSocket.CLOSED) resolve();
    else customerSocket.once('close', () => resolve());
  });
});

async function waitFor(predicate: () => boolean, timeoutMs: number): Promise<void> {
  const start = Date.now();
  while (!predicate()) {
    if (Date.now() - start > timeoutMs) throw new Error(`waitFor timeout after ${timeoutMs}ms`);
    await new Promise((r) => setTimeout(r, 5));
  }
}
