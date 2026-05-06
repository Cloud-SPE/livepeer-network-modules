// Env-derived runtime config. Fail fast if anything required is missing.

export interface Config {
  brokerUrl: string;
  listenPort: number;
  defaultOffering: string;
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
  return {
    brokerUrl,
    listenPort,
    defaultOffering: process.env["LIVEPEER_DEFAULT_OFFERING"] ?? "default",
  };
}
