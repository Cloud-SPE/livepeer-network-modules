// Canonical Livepeer-* HTTP header names and error codes.
// Mirrors the Go side at
// capability-broker/internal/livepeerheader/headers.go and the spec at
// livepeer-network-protocol/headers/livepeer-headers.md.

/** Required + optional Livepeer-* HTTP header names (mixed-case canonical form). */
export const HEADER = {
  // Required request headers.
  CAPABILITY: "Livepeer-Capability",
  OFFERING: "Livepeer-Offering",
  PAYMENT: "Livepeer-Payment",
  SPEC_VERSION: "Livepeer-Spec-Version",
  MODE: "Livepeer-Mode",

  // Optional request header.
  REQUEST_ID: "Livepeer-Request-Id",

  // Response headers.
  BACKOFF: "Livepeer-Backoff",
  WORK_UNITS: "Livepeer-Work-Units",
  HEALTH_STATUS: "Livepeer-Health-Status",
  ERROR: "Livepeer-Error",
} as const;

/** Machine-readable error codes per the headers spec. */
export const ERROR_CODE = {
  CAPABILITY_NOT_SERVED: "capability_not_served",
  OFFERING_NOT_SERVED: "offering_not_served",
  PAYMENT_ENVELOPE_MISMATCH: "payment_envelope_mismatch",
  PAYMENT_INVALID: "payment_invalid",
  SPEC_VERSION_UNSUPPORTED: "spec_version_unsupported",
  MODE_UNSUPPORTED: "mode_unsupported",
  BACKEND_UNAVAILABLE: "backend_unavailable",
  CAPACITY_EXHAUSTED: "capacity_exhausted",
  INTERNAL_ERROR: "internal_error",
} as const;

/** Spec-wide major.minor this middleware speaks. */
export const SPEC_VERSION = "0.1";

export type ErrorCode = (typeof ERROR_CODE)[keyof typeof ERROR_CODE];
