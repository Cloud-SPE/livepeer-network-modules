import { timingSafeEqual } from 'node:crypto';
import type {
  AdminAuthResolver,
  AdminAuthResolverRequest,
  AdminAuthResolverResult,
} from './types.js';

export interface BasicAdminAuthResolverDeps {
  user: string;
  pass: string;
  realm?: string;
}

export const BASIC_AUTH_REALM_DEFAULT = 'customer-portal-admin';

export function createBasicAdminAuthResolver(
  deps: BasicAdminAuthResolverDeps,
): AdminAuthResolver {
  const expectedUser = Buffer.from(deps.user, 'utf8');
  const expectedPass = Buffer.from(deps.pass, 'utf8');
  return {
    async resolve(req: AdminAuthResolverRequest): Promise<AdminAuthResolverResult | null> {
      const header = req.headers['authorization'];
      if (!header || !header.toLowerCase().startsWith('basic ')) return null;
      const payload = header.slice('basic '.length).trim();
      let decoded: string;
      try {
        decoded = Buffer.from(payload, 'base64').toString('utf8');
      } catch {
        return null;
      }
      const idx = decoded.indexOf(':');
      if (idx < 0) return null;
      const user = Buffer.from(decoded.slice(0, idx), 'utf8');
      const pass = Buffer.from(decoded.slice(idx + 1), 'utf8');
      if (
        user.length !== expectedUser.length ||
        pass.length !== expectedPass.length ||
        !timingSafeEqual(user, expectedUser) ||
        !timingSafeEqual(pass, expectedPass)
      ) {
        return null;
      }
      return { actor: deps.user };
    },
  };
}
