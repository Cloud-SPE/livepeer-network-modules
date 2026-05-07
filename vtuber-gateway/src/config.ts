import { z } from "zod";

export const ConfigSchema = z.object({
  listenPort: z.number().int().positive().default(3001),
  logLevel: z.string().default("info"),
  brokerUrl: z.string().url(),
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
  vtuberControlReconnectWindowMs: z.number().int().nonnegative().default(30_000),
  vtuberControlReconnectBufferMessages: z.number().int().positive().default(64),
  vtuberControlReconnectBufferBytes: z.number().int().positive().default(1 << 20),
});

export type Config = z.infer<typeof ConfigSchema>;

export function loadConfig(): Config {
  const brokerUrl = process.env["LIVEPEER_BROKER_URL"];
  if (brokerUrl === undefined || brokerUrl === "") {
    throw new Error("LIVEPEER_BROKER_URL env var is required");
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

  return ConfigSchema.parse({
    listenPort: parseInt(process.env["PORT"] ?? "3001", 10),
    logLevel: process.env["LOG_LEVEL"] ?? "info",
    brokerUrl,
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
  });
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
