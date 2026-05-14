import type { auth } from "@livepeer-network-modules/customer-portal";

type AuthResolver = auth.AuthResolver;
type AuthResolverRequest = auth.AuthResolverRequest;
type Caller = auth.Caller;

export function createChainedAuthResolver(
  ...resolvers: Array<AuthResolver | undefined>
): AuthResolver {
  const active = resolvers.filter((resolver): resolver is AuthResolver => Boolean(resolver));
  return {
    async resolve(req: AuthResolverRequest): Promise<Caller | null> {
      for (const resolver of active) {
        const caller = await resolver.resolve(req);
        if (caller) return caller;
      }
      return null;
    },
  };
}
