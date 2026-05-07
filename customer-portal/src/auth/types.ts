import type { ApiKeyRow } from '../repo/apiKeys.js';
import type { CustomerRow } from '../repo/customers.js';

export interface Caller {
  id: string;
  tier: string;
  rateLimitTier: string;
  metadata?: unknown;
}

export interface AuthenticatedCaller extends Caller {
  customer: CustomerRow;
  apiKey: ApiKeyRow;
}

export interface AuthResolverRequest {
  headers: Record<string, string | undefined>;
  ip: string;
}

export interface AuthResolver {
  resolve(req: AuthResolverRequest): Promise<Caller | null>;
}

export interface AdminAuthResolverRequest {
  headers: Record<string, string | undefined>;
  ip: string;
}

export interface AdminAuthResolverResult {
  actor: string;
}

export interface AdminAuthResolver {
  resolve(req: AdminAuthResolverRequest): Promise<AdminAuthResolverResult | null>;
}
