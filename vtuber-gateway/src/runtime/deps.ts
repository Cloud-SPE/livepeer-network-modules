import type { AuthResolver } from "@livepeer-network-modules/customer-portal/auth";

import type { Config } from "../config.js";
import type { Db as VtuberDb } from "../db/pool.js";
import type { PayerDaemonClient } from "../providers/payerDaemon.js";
import type { ServiceRegistryClient } from "../providers/serviceRegistry.js";
import type { WorkerClient } from "../providers/workerClient.js";
import type { ReconnectWindow } from "../service/relay/reconnectWindow.js";
import type { SessionStore } from "../service/sessions/sessionStore.js";

export interface VtuberStripeClient {
  createCheckoutSession(input: {
    customerId: string;
    amountUsdCents: number;
    successUrl: string;
    cancelUrl: string;
  }): Promise<{ sessionId: string; url: string }>;
  constructEvent(
    rawBody: Buffer | string,
    signature: string,
  ): { id: string; type: string; data: { object: Record<string, unknown> } };
}

export interface VtuberWebhookEventStore {
  insertIfNew(eventId: string, type: string, payload: string): Promise<boolean>;
  creditTopup(input: {
    customerId: string;
    stripeSessionId: string;
    amountUsdCents: bigint;
  }): Promise<void>;
  markTopupDisputed(stripeSessionId: string): Promise<void>;
}

export interface VtuberGatewayDeps {
  cfg: Config;
  authResolver: AuthResolver;
  payerDaemon: PayerDaemonClient;
  serviceRegistry: ServiceRegistryClient;
  worker: WorkerClient;
  sessionStore: SessionStore;
  vtuberDb?: VtuberDb;
  stripe?: VtuberStripeClient;
  webhookEventStore?: VtuberWebhookEventStore;
  topupConfig?: { priceMinCents: number; priceMaxCents: number };
  reconnectWindow?: ReconnectWindow;
}
