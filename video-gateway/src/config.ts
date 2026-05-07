import { z } from "zod";

const ConfigSchema = z.object({
  brokerUrl: z.string().url(),
  brokerRtmpHost: z.string().min(1),
  listenPort: z.number().int().positive(),
  rtmpListenAddr: z.string().min(1),
  hlsBaseUrl: z.string().url(),
  payerDaemonSocket: z.string().min(1),
  databaseUrl: z.string().min(1),
  redisUrl: z.string().min(1),
  vodTusPath: z.string().min(1),
  webhookHmacPepper: z.string().min(1),
  staleStreamSweepIntervalSec: z.number().int().positive(),
  abrPolicy: z.enum(["customer-tier"]),
  customerPortalPepper: z.string().min(1),
  brokerCallTimeoutMs: z.number().int().positive(),
});

export type Config = z.infer<typeof ConfigSchema>;

export function loadConfig(): Config {
  const raw = {
    brokerUrl: process.env["LIVEPEER_BROKER_URL"],
    brokerRtmpHost: process.env["LIVEPEER_BROKER_RTMP_HOST"],
    listenPort: parseInt(process.env["PORT"] ?? "3000", 10),
    rtmpListenAddr: process.env["VIDEO_LIVE_RTMP_LISTEN_ADDR"] ?? ":1935",
    hlsBaseUrl: process.env["VIDEO_LIVE_HLS_BASE_URL"],
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
    brokerCallTimeoutMs: parseInt(process.env["BROKER_CALL_TIMEOUT_MS"] ?? "30000", 10),
  };
  return ConfigSchema.parse(raw);
}
