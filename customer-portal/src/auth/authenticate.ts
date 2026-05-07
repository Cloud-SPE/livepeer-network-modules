import type { Db } from '../db/pool.js';
import * as apiKeysRepo from '../repo/apiKeys.js';
import type { CustomerRow } from '../repo/customers.js';
import { TtlCache } from './cache.js';
import { API_KEY_PATTERN, hashApiKey } from './apiKey.js';
import {
  AccountClosedError,
  AccountSuspendedError,
  InvalidApiKeyError,
  MalformedAuthorizationError,
} from './errors.js';
import type { AuthenticatedCaller } from './types.js';

export interface AuthServiceConfig {
  pepper: string;
  cacheTtlMs: number;
}

export interface AuthServiceDeps {
  db: Db;
  config: AuthServiceConfig;
}

export interface AuthService {
  authenticate(authorizationHeader: string | undefined): Promise<AuthenticatedCaller>;
  invalidate(hash: string): void;
  readonly cacheSize: number;
}

export function createAuthService(deps: AuthServiceDeps): AuthService {
  const cache = new TtlCache<string, AuthenticatedCaller>(deps.config.cacheTtlMs);

  return {
    async authenticate(header) {
      const plaintext = parseBearer(header);
      const hash = hashApiKey(deps.config.pepper, plaintext);

      const cached = cache.get(hash);
      if (cached) {
        enforceActiveStatus(cached.customer);
        void markUsedAsync(deps.db, cached.apiKey.id);
        return cached;
      }

      const row = await apiKeysRepo.findActiveByHash(deps.db, hash);
      if (!row) throw new InvalidApiKeyError();

      enforceActiveStatus(row.customer);

      const caller: AuthenticatedCaller = {
        id: row.customer.id,
        tier: row.customer.tier,
        rateLimitTier: row.customer.rateLimitTier,
        customer: row.customer,
        apiKey: row.apiKey,
      };
      cache.set(hash, caller);
      void markUsedAsync(deps.db, row.apiKey.id);
      return caller;
    },

    invalidate(hash) {
      cache.delete(hash);
    },

    get cacheSize() {
      return cache.size;
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
  if (!API_KEY_PATTERN.test(token)) {
    throw new MalformedAuthorizationError('token format invalid');
  }
  return token;
}

function enforceActiveStatus(customer: CustomerRow): void {
  if (customer.status === 'suspended') throw new AccountSuspendedError(customer.id);
  if (customer.status === 'closed') throw new AccountClosedError(customer.id);
}

async function markUsedAsync(db: Db, apiKeyId: string): Promise<void> {
  try {
    await apiKeysRepo.markUsed(db, apiKeyId, new Date());
  } catch {
    // best-effort
  }
}
