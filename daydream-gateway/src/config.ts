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

const __dirname = dirname(fileURLToPath(import.meta.url));

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

  /** Path to proto-contracts repo root for protoLoader includes. */
  protoRoot: string;
}

const DEFAULT_PROTO_ROOT = resolve(
  __dirname,
  "..",
  "..",
  "livepeer-network-protocol",
  "proto",
);

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
    protoRoot: env.DAYDREAM_GATEWAY_PROTO_ROOT ?? DEFAULT_PROTO_ROOT,
  };
}
