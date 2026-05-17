/**
 * Environment + flag configuration for daydream-gateway.
 *
 * Keep this thin: the whole component is meant to be operator-runnable
 * with a handful of env vars and nothing more. If a knob feels like it
 * needs a UI to set, it probably belongs in scope-playground-ui or on
 * the orch's host-config.yaml, not here.
 */

import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { existsSync } from "node:fs";

const __dirname = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(__dirname, "..", "..");

export interface Config {
  /** HTTP listen address (e.g. ":9100" or "0.0.0.0:9100"). */
  listen: string;

  /** Path to payer-daemon's unix socket. Required. */
  payerDaemonSocket: string;

  /** Path to service-registry-daemon's unix socket. Required. */
  resolverSocket: string;

  /** Capability id we resolve orchs for. */
  capabilityId: string;

  /** Offering id we resolve orchs for. */
  offeringId: string;

  /** Interaction mode we expect from candidate orchs. */
  interactionMode: string;

  /**
   * Optional eth address to pin all sessions to. Useful for dev/debug.
   * When set, the gateway resolves only this orch, ignoring others.
   */
  pinnedOrchEthAddress?: string;

  /** Cache TTL for the resolver snapshot. */
  resolverSnapshotTtlMs: number;

  /** Path to livepeer-network-protocol/proto for payer-daemon protos. */
  paymentProtoRoot: string;

  /** Path to proto-contracts for resolver/service-registry protos. */
  resolverProtoRoot: string;

  /** Retryable failure threshold before a route cools down locally. */
  routeFailureThreshold: number;

  /** Cooldown duration for locally failing routes. */
  routeCooldownMs: number;
}

const DEFAULT_PAYMENT_PROTO_ROOT = firstExistingPath([
  resolve("/app", "proto"),
  resolve(REPO_ROOT, "livepeer-network-protocol", "proto"),
]);
const DEFAULT_RESOLVER_PROTO_ROOT = firstExistingPath([
  resolve("/app", "proto-contracts"),
  resolve(REPO_ROOT, "proto-contracts"),
]);

export function loadConfig(env: NodeJS.ProcessEnv = process.env): Config {
  const listen = env.DAYDREAM_GATEWAY_LISTEN ?? ":9100";
  const payerDaemonSocket = env.DAYDREAM_GATEWAY_PAYER_DAEMON_SOCKET;
  if (!payerDaemonSocket) {
    throw new Error(
      "DAYDREAM_GATEWAY_PAYER_DAEMON_SOCKET is required (path to payer-daemon unix socket)",
    );
  }
  const resolverSocket = env.DAYDREAM_GATEWAY_RESOLVER_SOCKET;
  if (!resolverSocket) {
    throw new Error(
      "DAYDREAM_GATEWAY_RESOLVER_SOCKET is required (path to service-registry-daemon unix socket)",
    );
  }
  return {
    listen,
    payerDaemonSocket,
    resolverSocket,
    capabilityId: env.DAYDREAM_GATEWAY_CAPABILITY_ID ?? "daydream:scope:v1",
    offeringId: env.DAYDREAM_GATEWAY_OFFERING_ID ?? "default",
    interactionMode:
      env.DAYDREAM_GATEWAY_INTERACTION_MODE ??
      "session-control-external-media@v0",
    pinnedOrchEthAddress: env.DAYDREAM_GATEWAY_PINNED_ORCH,
    resolverSnapshotTtlMs: Number(
      env.DAYDREAM_GATEWAY_RESOLVER_TTL_MS ?? "30000",
    ),
    paymentProtoRoot:
      env.DAYDREAM_GATEWAY_PAYMENT_PROTO_ROOT ?? DEFAULT_PAYMENT_PROTO_ROOT,
    resolverProtoRoot:
      env.DAYDREAM_GATEWAY_RESOLVER_PROTO_ROOT ?? DEFAULT_RESOLVER_PROTO_ROOT,
    routeFailureThreshold: Number(
      env.LIVEPEER_ROUTE_FAILURE_THRESHOLD ?? "2",
    ),
    routeCooldownMs: Number(
      env.LIVEPEER_ROUTE_COOLDOWN_MS ?? "30000",
    ),
  };
}

function firstExistingPath(paths: string[]): string {
  for (const candidate of paths) {
    if (existsSync(candidate)) return candidate;
  }
  return paths[0]!;
}
