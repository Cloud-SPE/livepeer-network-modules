/**
 * Client for payment-daemon (sender mode) over unix-socket gRPC.
 *
 * Modeled on openai-gateway/src/livepeer/payment.ts (per active plan
 * 0025) but stripped to the minimum daydream-gateway needs: one
 * createPayment RPC per session-open + topup.
 *
 * Idempotent init: first call dials + health-probes; later calls reuse
 * the cached client.
 */

import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";

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
  ticketParamsBaseUrl?: string;
}

interface CreatePaymentResponse {
  paymentBytes: Buffer;
  ticketsCreated: number;
  expectedValue: Buffer;
}

interface HealthResponse {
  status: string;
}

const PROTO_FILES = [
  "livepeer/payments/v1/types.proto",
  "livepeer/payments/v1/payer_daemon.proto",
];

// Default face-value per session-open mint. Sessions are
// seconds-elapsed at ~1.5M wei/second; this gives ~few minutes runway
// per ticket. Tune for production via env later.
const DEFAULT_FACE_VALUE_WEI = 100_000_000n;

let cachedClient: PayerDaemonClient | null = null;

export interface InitOptions {
  socketPath: string;
  protoRoot: string;
}

export async function init(opts: InitOptions): Promise<void> {
  if (cachedClient !== null) return;

  const def = await protoLoader.load(PROTO_FILES, {
    keepCase: false,
    longs: String,
    enums: String,
    defaults: true,
    oneofs: true,
    includeDirs: [opts.protoRoot],
  });
  const proto = grpc.loadPackageDefinition(def) as unknown as {
    livepeer: {
      payments: { v1: { PayerDaemon: grpc.ServiceClientConstructor } };
    };
  };
  const ClientCtor = proto.livepeer.payments.v1.PayerDaemon;
  const client = new ClientCtor(
    `unix:${opts.socketPath}`,
    grpc.credentials.createInsecure(),
  ) as unknown as PayerDaemonClient;

  await new Promise<void>((res, rej) => {
    client.health({}, (err) => (err ? rej(err) : res()));
  });
  cachedClient = client;
}

export function shutdown(): void {
  if (cachedClient) {
    cachedClient.close();
    cachedClient = null;
  }
}

export interface BuildPaymentInputs {
  capabilityId: string;
  offeringId: string;
  recipientHex: string;
  brokerUrl: string;
  faceValueWei?: bigint;
}

/**
 * Returns the base64-encoded `Livepeer-Payment` header value for the
 * supplied (capability, offering, recipient, broker) tuple. Throws
 * if `init()` hasn't been called.
 */
export async function buildPayment(
  inputs: BuildPaymentInputs,
): Promise<string> {
  if (!cachedClient) {
    throw new Error(
      "buildPayment: payer-daemon client not initialized; call init() first",
    );
  }
  const req: CreatePaymentRequest = {
    faceValue: bigintToBigEndian(inputs.faceValueWei ?? DEFAULT_FACE_VALUE_WEI),
    recipient: hexToBuffer(inputs.recipientHex),
    capability: inputs.capabilityId,
    offering: inputs.offeringId,
    ticketParamsBaseUrl: inputs.brokerUrl,
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
  const normalized = hex.trim().replace(/^0x/i, "");
  if (!/^[0-9a-fA-F]{40}$/.test(normalized)) {
    throw new Error(`invalid recipient hex address: ${hex}`);
  }
  return Buffer.from(normalized, "hex");
}
