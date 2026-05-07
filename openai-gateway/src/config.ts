import { loadOfferingsFromDisk, type OfferingsConfig } from "./service/offerings.js";

export interface Config {
  brokerUrl: string;
  listenPort: number;
  defaultOffering: string;
  payerDaemonSocket: string;
  offeringsConfigPath: string;
  offerings: OfferingsConfig;
  audioSpeechEnabled: boolean;
  brokerCallTimeoutMs: number;
}

export function loadConfig(): Config {
  const brokerUrl = process.env["LIVEPEER_BROKER_URL"];
  if (!brokerUrl) {
    throw new Error("LIVEPEER_BROKER_URL env var is required (e.g. http://broker:8080)");
  }
  const listenPort = parseInt(process.env["PORT"] ?? "3000", 10);
  if (Number.isNaN(listenPort) || listenPort <= 0) {
    throw new Error(`invalid PORT env var: ${process.env["PORT"]}`);
  }
  const offeringsConfigPath =
    process.env["OPENAI_DEFAULT_OFFERING_PER_CAPABILITY"] ??
    "/etc/openai-gateway/offerings.yaml";

  return {
    brokerUrl,
    listenPort,
    defaultOffering: process.env["LIVEPEER_DEFAULT_OFFERING"] ?? "default",
    payerDaemonSocket:
      process.env["LIVEPEER_PAYER_DAEMON_SOCKET"] ?? "/var/run/livepeer/payer-daemon.sock",
    offeringsConfigPath,
    offerings: loadOfferingsFromDisk(offeringsConfigPath),
    audioSpeechEnabled: parseBool(process.env["OPENAI_AUDIO_SPEECH_ENABLED"], false),
    brokerCallTimeoutMs: parseInt(process.env["BROKER_CALL_TIMEOUT_MS"] ?? "30000", 10),
  };
}

function parseBool(value: string | undefined, fallback: boolean): boolean {
  if (value === undefined) return fallback;
  return value === "1" || value.toLowerCase() === "true";
}
