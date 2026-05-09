import { existsSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { z } from "zod";

const __dirname = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(__dirname, "..", "..");
const DEFAULT_RESOLVER_PROTO_ROOT = firstExistingPath([
  resolve("/app", "proto-contracts"),
  resolve(REPO_ROOT, "proto-contracts"),
]);

const ConfigSchema = z.object({
  brokerUrl: z.string().url().nullable(),
  brokerRtmpHost: z.string().min(1),
  resolverSocket: z.string().min(1).nullable(),
  resolverProtoRoot: z.string().min(1),
  resolverSnapshotTtlMs: z.number().int().positive(),
  listenPort: z.number().int().positive(),
  rtmpListenAddr: z.string().min(1),
  hlsBaseUrl: z.string().url().nullable(),
  payerDaemonSocket: z.string().min(1),
  databaseUrl: z.string().min(1),
  redisUrl: z.string().min(1),
  vodTusPath: z.string().min(1),
  webhookHmacPepper: z.string().min(1),
  staleStreamSweepIntervalSec: z.number().int().positive(),
  abrPolicy: z.enum(["customer-tier"]),
  customerPortalPepper: z.string().min(1),
  adminTokens: z.array(z.string().min(1)),
  brokerCallTimeoutMs: z.number().int().positive(),
});

export type Config = z.infer<typeof ConfigSchema>;

export function loadConfig(): Config {
  const brokerUrl = process.env["LIVEPEER_BROKER_URL"] ?? null;
  const resolverSocket = process.env["LIVEPEER_RESOLVER_SOCKET"] ?? null;
  if (!brokerUrl && !resolverSocket) {
    throw new Error(
      "set either LIVEPEER_BROKER_URL for static routing or LIVEPEER_RESOLVER_SOCKET for manifest-driven routing",
    );
  }
  const raw = {
    brokerUrl,
    brokerRtmpHost: process.env["LIVEPEER_BROKER_RTMP_HOST"] ?? "capability-broker",
    resolverSocket,
    resolverProtoRoot:
      process.env["LIVEPEER_RESOLVER_PROTO_ROOT"] ?? DEFAULT_RESOLVER_PROTO_ROOT,
    resolverSnapshotTtlMs: parseInt(
      process.env["LIVEPEER_RESOLVER_SNAPSHOT_TTL_MS"] ?? "15000",
      10,
    ),
    listenPort: parseInt(process.env["PORT"] ?? "3000", 10),
    rtmpListenAddr: process.env["VIDEO_LIVE_RTMP_LISTEN_ADDR"] ?? ":1935",
    hlsBaseUrl: process.env["VIDEO_LIVE_HLS_BASE_URL"] ?? null,
    payerDaemonSocket:
      process.env["LIVEPEER_PAYER_DAEMON_SOCKET"] ?? "/var/run/livepeer/payer-daemon.sock",
    databaseUrl: process.env["DATABASE_URL"],
    redisUrl: process.env["REDIS_URL"],
    vodTusPath: process.env["VIDEO_VOD_TUS_PATH"] ?? "/v1/uploads",
    webhookHmacPepper: process.env["VIDEO_WEBHOOK_HMAC_PEPPER"] ?? "dev-pepper",
    staleStreamSweepIntervalSec: parseInt(
      process.env["VIDEO_STALE_STREAM_SWEEP_INTERVAL_SECONDS"] ?? "60",
      10,
    ),
    abrPolicy: process.env["VIDEO_GATEWAY_ABR_POLICY"] ?? "customer-tier",
    customerPortalPepper: process.env["CUSTOMER_PORTAL_PEPPER"] ?? "dev-pepper",
    adminTokens: parseCsv(process.env["VIDEO_GATEWAY_ADMIN_TOKENS"]),
    brokerCallTimeoutMs: parseInt(process.env["BROKER_CALL_TIMEOUT_MS"] ?? "30000", 10),
  };
  return ConfigSchema.parse(raw);
}

function parseCsv(value: string | undefined): string[] {
  if (!value) return [];
  return value
    .split(",")
    .map((part) => part.trim())
    .filter((part) => part.length > 0);
}

function firstExistingPath(paths: string[]): string {
  for (const candidate of paths) {
    if (existsSync(candidate)) return candidate;
  }
  return paths[0]!;
}
