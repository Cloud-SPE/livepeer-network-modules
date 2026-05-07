import type { Db } from './db/pool.js';
import {
  createAuthService,
  createApiKeyAuthResolver,
  createBasicAdminAuthResolver,
  type AuthResolver,
  type AuthService,
  type AdminAuthResolver,
} from './auth/index.js';
import { createPrepaidQuotaWallet } from './billing/index.js';
import type { Wallet } from './billing/types.js';
import {
  createDbWebhookEventStore,
  type StripeClient,
  type WebhookEventStore,
} from './billing/stripe/index.js';
import { createAdminEngine, type AdminEngine } from './admin/index.js';

export interface CreateCustomerPortalInput {
  db: Db;
  pepper: string;
  cacheTtlMs?: number;
  stripe?: StripeClient;
  admin?: {
    user: string;
    pass: string;
    realm?: string;
  };
}

export interface CustomerPortal {
  authService: AuthService;
  authResolver: AuthResolver;
  adminAuthResolver?: AdminAuthResolver;
  wallet: Wallet;
  webhookEventStore: WebhookEventStore;
  adminEngine: AdminEngine;
  stripe?: StripeClient;
}

export function createCustomerPortal(input: CreateCustomerPortalInput): CustomerPortal {
  const authService = createAuthService({
    db: input.db,
    config: {
      pepper: input.pepper,
      cacheTtlMs: input.cacheTtlMs ?? 30_000,
    },
  });
  const authResolver = createApiKeyAuthResolver({ service: authService });
  const wallet = createPrepaidQuotaWallet({ db: input.db });
  const webhookEventStore = createDbWebhookEventStore(input.db);
  const adminEngine = createAdminEngine({ db: input.db });

  const portal: CustomerPortal = {
    authService,
    authResolver,
    wallet,
    webhookEventStore,
    adminEngine,
  };
  if (input.admin) {
    portal.adminAuthResolver = createBasicAdminAuthResolver({
      user: input.admin.user,
      pass: input.admin.pass,
      ...(input.admin.realm !== undefined ? { realm: input.admin.realm } : {}),
    });
  }
  if (input.stripe) {
    portal.stripe = input.stripe;
  }
  return portal;
}

export * as auth from './auth/index.js';
export * as billing from './billing/index.js';
export * as payment from './payment/index.js';
export * as registry from './registry/index.js';
export * as middleware from './middleware/index.js';
export * as admin from './admin/index.js';
export * as db from './db/index.js';
export * as testing from './testing/index.js';
