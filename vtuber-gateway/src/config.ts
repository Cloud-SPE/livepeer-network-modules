import { existsSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { z } from "zod";

const __dirname = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(__dirname, "..", "..");
const DEFAULT_PAYMENT_PROTO_ROOT = firstExistingPath([
  resolve("/app", "proto"),
  resolve(REPO_ROOT, "livepeer-network-protocol", "proto"),
]);
const DEFAULT_RESOLVER_PROTO_ROOT = firstExistingPath([
  resolve("/app", "proto-contracts"),
  resolve(REPO_ROOT, "proto-contracts"),
]);

export const ConfigSchema = z.object({
  listenPort: z.number().int().positive().default(3001),
  logLevel: z.string().default("info"),
  brokerUrl: z.string().url().nullable().default(null),
  resolverSocket: z.string().min(1).nullable().default(null),
  paymentProtoRoot: z.string().min(1).default("/app/proto"),
  resolverProtoRoot: z.string().min(1).default("/app/proto-contracts"),
  resolverSnapshotTtlMs: z.number().int().positive().default(15_000),
  payerDaemonSocket: z.string().min(1),
  payerDefaultFaceValueWei: z.string().regex(/^\d+$/),
  vtuberDefaultOffering: z.string().default("default"),
  vtuberSessionDefaultTtlSeconds: z.number().int().positive().default(3600),
  vtuberSessionBearerTtlSeconds: z.number().int().positive().default(7200),
  vtuberRateCardUsdPerSecond: z.string().regex(/^\d+(\.\d+)?$/).default("0.01"),
  vtuberWorkerCallTimeoutMs: z.number().int().positive().default(15_000),
  vtuberRelayMaxPerSession: z.number().int().positive().default(8),
  vtuberSessionBearerPepper: z.string().min(16),
  vtuberWorkerControlBearerPepper: z.string().min(16),
  databaseUrl: z.string().min(1).default("postgres://localhost/vtuber_gateway"),
  customerPortalPepper: z.string().min(1).default("dev-pepper"),
  vtuberControlReconnectWindowMs: z.number().int().nonnegative().default(30_000),
  vtuberControlReconnectBufferMessages: z.number().int().positive().default(64),
  vtuberControlReconnectBufferBytes: z.number().int().positive().default(1 << 20),
  adminTokens: z.array(z.string().min(1)).default([]),
});

export type Config = z.infer<typeof ConfigSchema>;

export function loadConfig(): Config {
  const brokerUrl = process.env["LIVEPEER_BROKER_URL"];
  const resolverSocket = process.env["LIVEPEER_RESOLVER_SOCKET"] ?? null;
  if ((!brokerUrl || brokerUrl === "") && !resolverSocket) {
    throw new Error(
      "set either LIVEPEER_BROKER_URL for static routing or LIVEPEER_RESOLVER_SOCKET for manifest-driven routing",
    );
  }
  const payerSocket = process.env["LIVEPEER_PAYER_DAEMON_SOCKET"];
  if (payerSocket === undefined || payerSocket === "") {
    throw new Error("LIVEPEER_PAYER_DAEMON_SOCKET env var is required");
  }
  const faceValue = process.env["LIVEPEER_PAYER_DEFAULT_FACE_VALUE_WEI"];
  if (faceValue === undefined || faceValue === "") {
    throw new Error("LIVEPEER_PAYER_DEFAULT_FACE_VALUE_WEI env var is required");
  }
  const sessionPepper = process.env["VTUBER_SESSION_BEARER_PEPPER"];
  if (sessionPepper === undefined || sessionPepper.length < 16) {
    throw new Error(
      "VTUBER_SESSION_BEARER_PEPPER env var is required (>=16 chars)",
    );
  }
  const workerPepper = process.env["VTUBER_WORKER_CONTROL_BEARER_PEPPER"];
  if (workerPepper === undefined || workerPepper.length < 16) {
    throw new Error(
      "VTUBER_WORKER_CONTROL_BEARER_PEPPER env var is required (>=16 chars)",
    );
  }
  const databaseUrl = process.env["DATABASE_URL"];
  if (databaseUrl === undefined || databaseUrl === "") {
    throw new Error("DATABASE_URL env var is required");
  }
  const customerPortalPepper = process.env["CUSTOMER_PORTAL_PEPPER"];
  if (customerPortalPepper === undefined || customerPortalPepper === "") {
    throw new Error("CUSTOMER_PORTAL_PEPPER env var is required");
  }

  return ConfigSchema.parse({
    listenPort: parseInt(process.env["PORT"] ?? "3001", 10),
    logLevel: process.env["LOG_LEVEL"] ?? "info",
    brokerUrl: brokerUrl ?? null,
    resolverSocket,
    paymentProtoRoot:
      process.env["LIVEPEER_PAYMENT_PROTO_ROOT"] ?? DEFAULT_PAYMENT_PROTO_ROOT,
    resolverProtoRoot:
      process.env["LIVEPEER_RESOLVER_PROTO_ROOT"] ?? DEFAULT_RESOLVER_PROTO_ROOT,
    resolverSnapshotTtlMs: parseInt(
      process.env["LIVEPEER_RESOLVER_SNAPSHOT_TTL_MS"] ?? "15000",
      10,
    ),
    payerDaemonSocket: payerSocket,
    payerDefaultFaceValueWei: faceValue,
    vtuberDefaultOffering:
      process.env["VTUBER_DEFAULT_OFFERING"] ?? "default",
    vtuberSessionDefaultTtlSeconds: parseInt(
      process.env["VTUBER_SESSION_DEFAULT_TTL_SECONDS"] ?? "3600",
      10,
    ),
    vtuberSessionBearerTtlSeconds: parseInt(
      process.env["VTUBER_SESSION_BEARER_TTL_SECONDS"] ?? "7200",
      10,
    ),
    vtuberRateCardUsdPerSecond:
      process.env["VTUBER_RATE_CARD_USD_PER_SECOND"] ?? "0.01",
    vtuberWorkerCallTimeoutMs: parseInt(
      process.env["VTUBER_WORKER_CALL_TIMEOUT_MS"] ?? "15000",
      10,
    ),
    vtuberRelayMaxPerSession: parseInt(
      process.env["VTUBER_RELAY_MAX_PER_SESSION"] ?? "8",
      10,
    ),
    vtuberSessionBearerPepper: sessionPepper,
    vtuberWorkerControlBearerPepper: workerPepper,
    databaseUrl,
    customerPortalPepper,
    vtuberControlReconnectWindowMs: parseDurationMs(
      process.env["VTUBER_CONTROL_RECONNECT_WINDOW"] ?? "30s",
    ),
    vtuberControlReconnectBufferMessages: parseInt(
      process.env["VTUBER_CONTROL_RECONNECT_BUFFER_MESSAGES"] ?? "64",
      10,
    ),
    vtuberControlReconnectBufferBytes: parseInt(
      process.env["VTUBER_CONTROL_RECONNECT_BUFFER_BYTES"] ?? "1048576",
      10,
    ),
    adminTokens: parseCsv(process.env["VTUBER_GATEWAY_ADMIN_TOKENS"] ?? ""),
  });
}

function firstExistingPath(paths: string[]): string {
  for (const candidate of paths) {
    if (existsSync(candidate)) return candidate;
  }
  return paths[0]!;
}

export function parseDurationMs(value: string): number {
  const trimmed = value.trim();
  if (trimmed === "") {
    return 0;
  }
  const m = trimmed.match(/^(\d+)(ms|s|m)?$/);
  if (m === null) {
    throw new Error(`invalid duration: ${value}`);
  }
  const n = parseInt(m[1]!, 10);
  switch (m[2]) {
    case "ms":
      return n;
    case "m":
      return n * 60_000;
    case "s":
    case undefined:
      return n * 1000;
    default:
      throw new Error(`invalid duration unit: ${m[2]}`);
  }
}

function parseCsv(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter((item) => item.length > 0);
}
