import type { AuthResolver, AuthResolverRequest, Caller } from './types.js';
import type { AuthService } from './authenticate.js';

export interface ApiKeyAuthResolverDeps {
  service: AuthService;
}

export function createApiKeyAuthResolver(deps: ApiKeyAuthResolverDeps): AuthResolver {
  return {
    async resolve(req: AuthResolverRequest): Promise<Caller | null> {
      const header = req.headers['authorization'];
      try {
        const caller = await deps.service.authenticate(header);
        return caller;
      } catch {
        return null;
      }
    },
  };
}
