import { createHash } from 'node:crypto';
import { InvalidApiKeyError, MalformedAuthorizationError } from './errors.js';
import type {
  AdminAuthResolver,
  AdminAuthResolverRequest,
  AdminAuthResolverResult,
} from './types.js';

export interface StaticAdminTokenAuthResolverDeps {
  tokens: readonly string[];
}

export function createStaticAdminTokenAuthResolver(
  deps: StaticAdminTokenAuthResolverDeps,
): AdminAuthResolver {
  const allowed = new Set(
    deps.tokens.filter((token) => token.trim().length > 0).map((token) => sha256(token.trim())),
  );

  return {
    async resolve(req: AdminAuthResolverRequest): Promise<AdminAuthResolverResult | null> {
      try {
        const token = parseBearer(req.headers['authorization']);
        const actor = parseActor(req.headers['x-actor']);
        if (!allowed.has(sha256(token))) return null;
        return { actor };
      } catch (err) {
        if (err instanceof InvalidApiKeyError || err instanceof MalformedAuthorizationError) {
          return null;
        }
        throw err;
      }
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
  return token;
}

function parseActor(actor: string | undefined): string {
  const value = actor?.trim();
  if (!value) throw new InvalidApiKeyError();
  return value;
}

function sha256(input: string): string {
  return createHash('sha256').update(input).digest('hex');
}
