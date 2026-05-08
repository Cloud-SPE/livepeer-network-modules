// Mints `Livepeer-Payment` header values by calling
// PayerDaemon.CreatePayment over a unix-socket gRPC connection.
//
// v0.2 architectural shift: the gateway no longer hand-rolls Payment
// proto bytes. The daemon is the canonical owner of envelope encoding —
// once warm-key handling lands (plan 0017), the gateway being able to
// sign tickets locally would itself be a key-handling surface we don't
// want.

import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";

const __dirname = dirname(fileURLToPath(import.meta.url));

const PROTO_ROOT = resolve(__dirname, "..", "..", "..", "livepeer-network-protocol", "proto");

const PROTO_FILES = [
  "livepeer/payments/v1/types.proto",
  "livepeer/payments/v1/payer_daemon.proto",
];

interface PayerDaemonClient extends grpc.Client {
  createPayment(
    req: CreatePaymentRequest,
    cb: (err: grpc.ServiceError | null, resp: CreatePaymentResponse) => void,
  ): void;
  health(
    req: Record<string, never>,
    cb: (err: grpc.ServiceError | null, resp: HealthResponse) => void,
  ): void;
}

interface CreatePaymentRequest {
  faceValue: Buffer;
  recipient: Buffer;
  capability: string;
  offering: string;
}

interface CreatePaymentResponse {
  paymentBytes: Buffer;
  ticketsCreated: number;
  expectedValue: Buffer;
}

interface HealthResponse {
  status: string;
}

// 20-byte recipient placeholder; the payer-daemon doesn't validate the
// address in v0.2 (chain integration is plan 0016) and the receiver
// embeds whatever the daemon sends.
const DEFAULT_RECIPIENT_HEX = "1234567890abcdef1234567890abcdef12345678";

// Target spend per request. The receiver may answer with a larger
// face_value × lower win_prob per the quote-free flow; the gateway
// doesn't care.
const DEFAULT_FACE_VALUE_WEI = 1000n;

let cachedClient: PayerDaemonClient | null = null;

interface InitOptions {
  socketPath: string;
  protoRoot?: string;
}

/**
 * Dial the payer-daemon at `socketPath` and Health-probe it.
 * Idempotent — second calls reuse the existing client.
 */
export async function init(opts: InitOptions): Promise<void> {
  if (cachedClient !== null) return;

  const def = await protoLoader.load(PROTO_FILES, {
    keepCase: false,
    longs: String,
    enums: String,
    defaults: true,
    oneofs: true,
    includeDirs: [opts.protoRoot ?? PROTO_ROOT],
  });
  const proto = grpc.loadPackageDefinition(def) as unknown as {
    livepeer: { payments: { v1: { PayerDaemon: grpc.ServiceClientConstructor } } };
  };
  const ClientCtor = proto.livepeer.payments.v1.PayerDaemon;
  const client = new ClientCtor(
    `unix:${opts.socketPath}`,
    grpc.credentials.createInsecure(),
  ) as unknown as PayerDaemonClient;

  // Health probe.
  await new Promise<void>((res, rej) => {
    client.health({}, (err) => (err ? rej(err) : res()));
  });
  cachedClient = client;
}

/** Closes the cached gRPC client. Optional; the OS reaps it on exit. */
export function shutdown(): void {
  if (cachedClient) {
    cachedClient.close();
    cachedClient = null;
  }
}

/**
 * Returns the base64-encoded `Livepeer-Payment` header value for the
 * given (capability, offering). Throws if `init()` hasn't been called.
 */
export async function buildPayment(inputs: {
  capabilityId: string;
  offeringId: string;
}): Promise<string> {
  if (!cachedClient) {
    throw new Error("buildPayment: payer-daemon client not initialized; call init() first");
  }
  const req: CreatePaymentRequest = {
    faceValue: bigintToBigEndian(DEFAULT_FACE_VALUE_WEI),
    recipient: hexToBuffer(DEFAULT_RECIPIENT_HEX),
    capability: inputs.capabilityId,
    offering: inputs.offeringId,
  };
  const resp = await new Promise<CreatePaymentResponse>((res, rej) => {
    cachedClient!.createPayment(req, (err, r) => (err ? rej(err) : res(r)));
  });
  return Buffer.from(resp.paymentBytes).toString("base64");
}

function bigintToBigEndian(n: bigint): Buffer {
  if (n === 0n) return Buffer.alloc(0);
  const bytes: number[] = [];
  let v = n;
  while (v > 0n) {
    bytes.unshift(Number(v & 0xffn));
    v >>= 8n;
  }
  return Buffer.from(bytes);
}

function hexToBuffer(hex: string): Buffer {
  return Buffer.from(hex, "hex");
}
