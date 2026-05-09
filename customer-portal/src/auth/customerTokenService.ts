import type { Db } from '../db/pool.js';
import * as authTokensRepo from '../repo/authTokens.js';
import type { CustomerRow } from '../repo/customers.js';
import { TtlCache } from './cache.js';
import {
  AccountClosedError,
  AccountSuspendedError,
  InvalidApiKeyError,
  MalformedAuthorizationError,
} from './errors.js';
import { UI_AUTH_TOKEN_PATTERN, generateUiAuthToken, hashUiAuthToken } from './uiToken.js';

export interface CustomerUiSession {
  customer: CustomerRow;
  authToken: authTokensRepo.AuthTokenRow;
}

export interface CustomerTokenServiceConfig {
  pepper: string;
  cacheTtlMs: number;
  envPrefix: 'live' | 'test';
}

export interface CustomerTokenServiceDeps {
  db: Db;
  config: CustomerTokenServiceConfig;
}

export interface IssueCustomerTokenInput {
  customerId: string;
  label?: string | null;
}

export interface IssueCustomerTokenResult {
  tokenId: string;
  plaintext: string;
}

export interface CustomerTokenService {
  authenticate(authorizationHeader: string | undefined): Promise<CustomerUiSession>;
  list(customerId: string): Promise<authTokensRepo.AuthTokenRow[]>;
  issue(input: IssueCustomerTokenInput): Promise<IssueCustomerTokenResult>;
  revoke(tokenId: string): Promise<void>;
  countActive(customerId: string): Promise<number>;
  invalidate(hash: string): void;
}

export function createCustomerTokenService(deps: CustomerTokenServiceDeps): CustomerTokenService {
  const cache = new TtlCache<string, CustomerUiSession>(deps.config.cacheTtlMs);

  return {
    async authenticate(header) {
      const plaintext = parseBearer(header);
      const hash = hashUiAuthToken(deps.config.pepper, plaintext);

      const cached = cache.get(hash);
      if (cached) {
        enforceActiveStatus(cached.customer);
        void markUsedAsync(deps.db, cached.authToken.id);
        return cached;
      }

      const row = await authTokensRepo.findActiveByHash(deps.db, hash);
      if (!row) throw new InvalidApiKeyError();

      enforceActiveStatus(row.customer);

      const session: CustomerUiSession = {
        customer: row.customer,
        authToken: row.authToken,
      };
      cache.set(hash, session);
      void markUsedAsync(deps.db, row.authToken.id);
      return session;
    },

    async list(customerId) {
      return authTokensRepo.findByCustomer(deps.db, customerId);
    },

    async issue(input) {
      const plaintext = generateUiAuthToken(deps.config.envPrefix);
      const row = await authTokensRepo.insertAuthToken(deps.db, {
        customerId: input.customerId,
        hash: hashUiAuthToken(deps.config.pepper, plaintext),
        ...(input.label !== undefined ? { label: input.label } : {}),
      });
      return { tokenId: row.id, plaintext };
    },

    async revoke(tokenId) {
      await authTokensRepo.revoke(deps.db, tokenId, new Date());
    },

    async countActive(customerId) {
      return authTokensRepo.countActiveByCustomer(deps.db, customerId);
    },

    invalidate(hash) {
      cache.delete(hash);
    },
  };
}

function parseBearer(header: string | undefined): string {
  if (!header) throw new MalformedAuthorizationError('missing header');
  const [scheme, token, ...rest] = header.trim().split(/\s+/);
  if (scheme?.toLowerCase() !== 'bearer') {
    throw new MalformedAuthorizationError('expected Bearer scheme');
  }
  if (!token || rest.length > 0) {
    throw new MalformedAuthorizationError('expected exactly one token');
  }
  if (!UI_AUTH_TOKEN_PATTERN.test(token)) {
    throw new MalformedAuthorizationError('token format invalid');
  }
  return token;
}

function enforceActiveStatus(customer: CustomerRow): void {
  if (customer.status === 'suspended') throw new AccountSuspendedError(customer.id);
  if (customer.status === 'closed') throw new AccountClosedError(customer.id);
}

async function markUsedAsync(db: Db, tokenId: string): Promise<void> {
  try {
    await authTokensRepo.markUsed(db, tokenId, new Date());
  } catch {
    // best-effort
  }
}
