export {
  generateApiKey,
  hashApiKey,
  verifyApiKey,
  API_KEY_PATTERN,
  type EnvPrefix,
} from './apiKey.js';
export { TtlCache } from './cache.js';
export {
  MalformedAuthorizationError,
  InvalidApiKeyError,
  AccountSuspendedError,
  AccountClosedError,
} from './errors.js';
export {
  issueKey,
  revokeKey,
  type IssueKeyInput,
  type IssueKeyResult,
} from './keys.js';
export {
  createAuthService,
  type AuthService,
  type AuthServiceConfig,
  type AuthServiceDeps,
} from './authenticate.js';
export {
  createApiKeyAuthResolver,
  type ApiKeyAuthResolverDeps,
} from './apiKeyAuthResolver.js';
export {
  createBasicAdminAuthResolver,
  BASIC_AUTH_REALM_DEFAULT,
  type BasicAdminAuthResolverDeps,
} from './adminBasicAuth.js';
export {
  generateUiAuthToken,
  hashUiAuthToken,
  verifyUiAuthToken,
  UI_AUTH_TOKEN_PATTERN,
} from './uiToken.js';
export {
  createCustomerTokenService,
  type CustomerUiSession,
  type CustomerTokenService,
  type CustomerTokenServiceConfig,
  type CustomerTokenServiceDeps,
  type IssueCustomerTokenInput,
  type IssueCustomerTokenResult,
} from './customerTokenService.js';
export {
  createUiAuthResolver,
  type UiAuthResolverDeps,
} from './uiAuthResolver.js';
export {
  createStaticAdminTokenAuthResolver,
  type StaticAdminTokenAuthResolverDeps,
} from './adminTokenAuth.js';
export type {
  Caller,
  AuthenticatedCaller,
  AuthResolver,
  AuthResolverRequest,
  AdminAuthResolver,
  AdminAuthResolverRequest,
  AdminAuthResolverResult,
} from './types.js';
