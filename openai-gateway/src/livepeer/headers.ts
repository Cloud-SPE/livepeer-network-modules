// Inlined Livepeer-* header constants. Mirrors
// @tztcloud/livepeer-gateway-middleware src/headers.ts; will be replaced
// with the package import once npm-workspace plumbing lands (tech-debt).

export const HEADER = {
  CAPABILITY: "Livepeer-Capability",
  OFFERING: "Livepeer-Offering",
  PAYMENT: "Livepeer-Payment",
  SPEC_VERSION: "Livepeer-Spec-Version",
  MODE: "Livepeer-Mode",
  REQUEST_ID: "Livepeer-Request-Id",
  BACKOFF: "Livepeer-Backoff",
  WORK_UNITS: "Livepeer-Work-Units",
  ERROR: "Livepeer-Error",
} as const;

export const SPEC_VERSION = "0.1";
