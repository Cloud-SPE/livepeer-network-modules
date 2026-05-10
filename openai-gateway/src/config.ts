import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { existsSync } from "node:fs";

import { loadOfferingsFromDisk, type OfferingsConfig } from "./service/offerings.js";

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

export interface Config {
  brokerUrl: string | null;
  resolverSocket: string | null;
  recipientHex: string | null;
  listenPort: number;
  databaseUrl: string;
  authPepper: string;
  adminTokens: string[];
  publicBaseUrl: string | null;
  stripe: {
    secretKey: string;
    webhookSecret: string;
    topupMinCents: number;
    topupMaxCents: number;
  } | null;
  defaultOffering: string;
  payerDaemonSocket: string;
  paymentProtoRoot: string;
  resolverProtoRoot: string;
  resolverSnapshotTtlMs: number;
  offeringsConfigPath: string;
  offerings: OfferingsConfig;
  audioSpeechEnabled: boolean;
  brokerCallTimeoutMs: number;
}

export function loadConfig(): Config {
  const brokerUrl = process.env["LIVEPEER_BROKER_URL"];
  const resolverSocket = process.env["LIVEPEER_RESOLVER_SOCKET"] ?? null;
  if (!brokerUrl && !resolverSocket) {
    throw new Error(
      "set either LIVEPEER_BROKER_URL for static routing or LIVEPEER_RESOLVER_SOCKET for manifest-driven routing",
    );
  }
  const listenPort = parseInt(process.env["PORT"] ?? "3000", 10);
  if (Number.isNaN(listenPort) || listenPort <= 0) {
    throw new Error(`invalid PORT env var: ${process.env["PORT"]}`);
  }
  const offeringsConfigPath =
    process.env["OPENAI_DEFAULT_OFFERING_PER_CAPABILITY"] ??
    "/etc/openai-gateway/offerings.yaml";

  return {
    brokerUrl: brokerUrl ?? null,
    resolverSocket,
    recipientHex: process.env["LIVEPEER_RECIPIENT_HEX"]?.trim() ?? null,
    listenPort,
    databaseUrl: requiredEnv("DATABASE_URL"),
    authPepper: process.env["CUSTOMER_PORTAL_AUTH_PEPPER"] ?? "dev-openai-gateway-pepper",
    adminTokens: loadAdminTokens(),
    publicBaseUrl: process.env["OPENAI_GATEWAY_PUBLIC_BASE_URL"] ?? null,
    stripe: loadStripeConfig(),
    defaultOffering: process.env["LIVEPEER_DEFAULT_OFFERING"] ?? "default",
    payerDaemonSocket:
      process.env["LIVEPEER_PAYER_DAEMON_SOCKET"] ?? "/var/run/livepeer/payer-daemon.sock",
    paymentProtoRoot: process.env["LIVEPEER_PAYMENT_PROTO_ROOT"] ?? DEFAULT_PAYMENT_PROTO_ROOT,
    resolverProtoRoot: process.env["LIVEPEER_RESOLVER_PROTO_ROOT"] ?? DEFAULT_RESOLVER_PROTO_ROOT,
    resolverSnapshotTtlMs: parseInt(process.env["LIVEPEER_RESOLVER_SNAPSHOT_TTL_MS"] ?? "15000", 10),
    offeringsConfigPath,
    offerings: loadOfferingsFromDisk(offeringsConfigPath),
    audioSpeechEnabled: parseBool(process.env["OPENAI_AUDIO_SPEECH_ENABLED"], false),
    brokerCallTimeoutMs: parseInt(process.env["BROKER_CALL_TIMEOUT_MS"] ?? "30000", 10),
  };
}

function parseCsv(value: string | undefined): string[] {
  if (!value) return [];
  return value
    .split(',')
    .map((part) => part.trim())
    .filter((part) => part.length > 0);
}

function loadAdminTokens(): string[] {
  return parseCsv(process.env["OPENAI_GATEWAY_ADMIN_TOKENS"]);
}

function requiredEnv(name: string): string {
  const value = process.env[name];
  if (!value || value.trim().length === 0) {
    throw new Error(`missing required env var: ${name}`);
  }
  return value;
}

function loadStripeConfig():
  | {
      secretKey: string;
      webhookSecret: string;
      topupMinCents: number;
      topupMaxCents: number;
    }
  | null {
  const secretKey = process.env["STRIPE_SECRET_KEY"]?.trim();
  const webhookSecret = process.env["STRIPE_WEBHOOK_SECRET"]?.trim();
  if (!secretKey || !webhookSecret) return null;
  return {
    secretKey,
    webhookSecret,
    topupMinCents: parseInt(process.env["STRIPE_TOPUP_MIN_CENTS"] ?? "500", 10),
    topupMaxCents: parseInt(process.env["STRIPE_TOPUP_MAX_CENTS"] ?? "100000", 10),
  };
}

function parseBool(value: string | undefined, fallback: boolean): boolean {
  if (value === undefined) return fallback;
  return value === "1" || value.toLowerCase() === "true";
}

function firstExistingPath(paths: string[]): string {
  for (const candidate of paths) {
    if (existsSync(candidate)) return candidate;
  }
  return paths[0]!;
}
