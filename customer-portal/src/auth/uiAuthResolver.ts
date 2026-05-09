import { InvalidApiKeyError } from './errors.js';
import type { CustomerTokenService } from './customerTokenService.js';
import type { AuthResolver, AuthResolverRequest, Caller } from './types.js';

export interface UiAuthResolverDeps {
  service: CustomerTokenService;
}

export function createUiAuthResolver(deps: UiAuthResolverDeps): AuthResolver {
  return {
    async resolve(req: AuthResolverRequest): Promise<Caller | null> {
      try {
        const session = await deps.service.authenticate(req.headers['authorization']);
        return {
          id: session.customer.id,
          tier: session.customer.tier,
          rateLimitTier: session.customer.rateLimitTier,
          metadata: {
            email: session.customer.email,
            uiAuthTokenId: session.authToken.id,
          },
        };
      } catch (err) {
        if (err instanceof InvalidApiKeyError) return null;
        throw err;
      }
    },
  };
}
