// Runtime helpers — composition glue used by the index.ts main and the
// future routes/ + service/ layers (landing in plan 0013-vtuber phase 4).
// This module currently re-exports the customer-portal entrypoint so
// vtuber-gateway code paths can import shared shell primitives via a
// single facade.

export * as customerPortal from "@livepeer-rewrite/customer-portal";
