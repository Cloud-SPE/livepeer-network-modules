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
import { createHash } from "node:crypto";

import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";
import { isInvalidRecipientRandPaymentError } from "./errors.js";

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
  reportPaymentResult(
    req: ReportPaymentResultRequest,
    cb: (err: grpc.ServiceError | null, resp: Record<string, never>) => void,
  ): void;
  health(
    req: Record<string, never>,
    cb: (err: grpc.ServiceError | null, resp: HealthResponse) => void,
  ): void;
}

interface CreatePaymentRequest {
  recipient: Buffer;
  ticketParamsBaseUrl?: string;
  acceptedPrice: AcceptedPrice;
  funding: FundingIntent;
}

interface CreatePaymentResponse {
  paymentBytes: Buffer;
  ticketsCreated: number;
  expectedValue: Buffer;
  workId?: string;
}

interface BigUInt {
  value: Buffer;
}

interface QuoteRef {
  quoteId: string;
  quoteVersion: string;
  constraintFingerprint: Buffer;
  routeFingerprint: Buffer;
}

interface AcceptedPrice {
  pricePerUnitWei: BigUInt;
  unitsPerPrice: string;
  workUnitName: string;
  capability: string;
  offering: string;
  quoteRef: QuoteRef;
}

interface FundingIntent {
  estimatedUnits: string;
  fundedValueWei: BigUInt;
  maxTotalUnits: string;
  topUpAllowed?: boolean;
}

interface ReportPaymentResultRequest {
  workId: string;
  capability: string;
  offering: string;
  rejectionReason: string;
}

interface HealthResponse {
  status: string;
}

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
  recipientHex: string;
  brokerUrl?: string;
  pricePerUnitWei: string;
  workUnit: string;
  routeFingerprintSource?: unknown;
  constraintFingerprintSource?: unknown;
}): Promise<{ paymentBlob: string; workId: string }> {
  if (!cachedClient) {
    throw new Error("buildPayment: payer-daemon client not initialized; call init() first");
  }
  if (!inputs.workUnit.trim()) {
    throw new Error(`buildPayment: missing work unit for ${inputs.capabilityId}/${inputs.offeringId}`);
  }
  const pricePerUnitWei = parsePositiveBigInt(inputs.pricePerUnitWei, "pricePerUnitWei");
  const constraintFingerprint = sha256Canonical(inputs.constraintFingerprintSource ?? {});
  const routeFingerprint = sha256Canonical(
    inputs.routeFingerprintSource ?? {
      brokerUrl: inputs.brokerUrl ?? "",
      recipientHex: normalizeHex(inputs.recipientHex),
      capability: inputs.capabilityId,
      offering: inputs.offeringId,
      workUnit: inputs.workUnit,
      pricePerUnitWei: inputs.pricePerUnitWei,
    },
  );
  const quoteId = `route:${routeFingerprint.toString("hex")}`;
  const fundedValueWei = pricePerUnitWei > DEFAULT_FACE_VALUE_WEI ? pricePerUnitWei : DEFAULT_FACE_VALUE_WEI;
  const req: CreatePaymentRequest = {
    recipient: hexToBuffer(inputs.recipientHex),
    ticketParamsBaseUrl: inputs.brokerUrl,
    acceptedPrice: {
      pricePerUnitWei: { value: bigintToBigEndian(pricePerUnitWei) },
      unitsPerPrice: "1",
      workUnitName: inputs.workUnit,
      capability: inputs.capabilityId,
      offering: inputs.offeringId,
      quoteRef: {
        quoteId,
        quoteVersion: "1",
        constraintFingerprint,
        routeFingerprint,
      },
    },
    funding: {
      estimatedUnits: "1",
      fundedValueWei: { value: bigintToBigEndian(fundedValueWei) },
      maxTotalUnits: "1",
      topUpAllowed: false,
    },
  };
  const resp = await new Promise<CreatePaymentResponse>((res, rej) => {
    cachedClient!.createPayment(req, (err, r) => (err ? rej(err) : res(r)));
  });
  return {
    paymentBlob: Buffer.from(resp.paymentBytes).toString("base64"),
    workId: resp.workId ?? "",
  };
}

export async function withReportedRotationRetry<T>(
  inputs: {
    capabilityId: string;
    offeringId: string;
    recipientHex: string;
    brokerUrl?: string;
    pricePerUnitWei: string;
    workUnit: string;
    routeFingerprintSource?: unknown;
    constraintFingerprintSource?: unknown;
  },
  send: (paymentBlob: string) => Promise<T>,
): Promise<T> {
  const first = await buildPayment(inputs);
  try {
    return await send(first.paymentBlob);
  } catch (err) {
    if (!isInvalidRecipientRandPaymentError(err)) throw err;
    await reportInvalidRecipientRand({
      workId: first.workId,
      capabilityId: inputs.capabilityId,
      offeringId: inputs.offeringId,
    });
    const retried = await buildPayment(inputs);
    return await send(retried.paymentBlob);
  }
}

async function reportInvalidRecipientRand(input: {
  workId: string;
  capabilityId: string;
  offeringId: string;
}): Promise<void> {
  if (!cachedClient) {
    throw new Error("reportInvalidRecipientRand: payer-daemon client not initialized; call init() first");
  }
  await new Promise<void>((resolve, reject) => {
    cachedClient!.reportPaymentResult(
      {
        workId: input.workId,
        capability: input.capabilityId,
        offering: input.offeringId,
        rejectionReason: "PAYMENT_REJECTION_REASON_INVALID_RECIPIENT_RAND",
      },
      (err) => {
        if (!err) {
          resolve();
          return;
        }
        if (err.code === grpc.status.ABORTED) {
          resolve();
          return;
        }
        reject(err);
      },
    );
  });
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
  const normalized = normalizeHex(hex);
  if (!/^[0-9a-fA-F]{40}$/.test(normalized)) {
    throw new Error(`invalid recipient hex address: ${hex}`);
  }
  return Buffer.from(normalized, "hex");
}

function normalizeHex(hex: string): string {
  return hex.trim().replace(/^0x/i, "");
}

function parsePositiveBigInt(raw: string, field: string): bigint {
  const normalized = raw.trim();
  if (!/^[0-9]+$/.test(normalized)) {
    throw new Error(`buildPayment: invalid ${field}=${raw}`);
  }
  const value = BigInt(normalized);
  if (value <= 0n) {
    throw new Error(`buildPayment: ${field} must be > 0`);
  }
  return value;
}

function sha256Canonical(value: unknown): Buffer {
  return createHash("sha256").update(canonicalJson(value)).digest();
}

function canonicalJson(value: unknown): string {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map((entry) => canonicalJson(entry)).join(",")}]`;
  }
  const obj = value as Record<string, unknown>;
  const keys = Object.keys(obj).sort();
  return `{${keys.map((key) => `${JSON.stringify(key)}:${canonicalJson(obj[key])}`).join(",")}}`;
}
