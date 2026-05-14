/**
 * Service-registry-daemon resolver client + random orch selection.
 *
 * Modeled on openai-gateway/src/service/routeSelector.ts but pared down:
 * no pricing filter (no customer-side rate-cap in a broadcaster gateway),
 * no preference-extra scoring, no caching nuance beyond a simple TTL.
 *
 * The selection algorithm is random with retry: pick one, hand it back,
 * caller can mark failed and ask for another (the gateway retries on
 * session-open failure).
 */

import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";

import type { Config } from "./config.js";

const RESOLVER_PROTO_FILES = [
  "livepeer/registry/v1/types.proto",
  "livepeer/registry/v1/resolver.proto",
];

interface ResolverClient extends grpc.Client {
  listKnown(
    req: Record<string, never>,
    cb: (err: grpc.ServiceError | null, resp: ListKnownResult) => void,
  ): void;
  resolveByAddress(
    req: ResolveByAddressRequest,
    cb: (err: grpc.ServiceError | null, resp: ResolveResult) => void,
  ): void;
}

interface ListKnownResult {
  entries: KnownEntry[];
}

interface KnownEntry {
  ethAddress: string;
}

interface ResolveByAddressRequest {
  ethAddress: string;
  allowLegacyFallback: boolean;
  allowUnsigned: boolean;
  forceRefresh: boolean;
}

interface ResolveResult {
  nodes: ResolverNode[];
}

interface ResolverNode {
  url: string;
  operatorAddress: string;
  enabled: boolean;
  capabilities: ResolverCapability[];
}

interface ResolverCapability {
  name: string;
  workUnit: string;
  offerings: ResolverOffering[];
}

interface ResolverOffering {
  id: string;
  pricePerWorkUnitWei: string;
}

export interface OrchCandidate {
  brokerUrl: string;
  ethAddress: string;
  capability: string;
  offering: string;
  workUnit: string;
  pricePerWorkUnitWei: string;
}

export interface OrchSelector {
  /** Return the full set of healthy candidates (cached for TTL). */
  list(): Promise<OrchCandidate[]>;
  /** Pick one candidate at random; throws if none available. */
  pickRandom(): Promise<OrchCandidate>;
}

interface CachedSnapshot {
  expiresAt: number;
  candidates: OrchCandidate[];
}

export function createOrchSelector(cfg: Config): OrchSelector {
  const client = newResolverClient(
    cfg.resolverSocket,
    cfg.resolverProtoRoot,
  );
  let cache: CachedSnapshot | null = null;

  async function load(): Promise<OrchCandidate[]> {
    const now = Date.now();
    if (cache && cache.expiresAt > now) return cache.candidates;

    const knownEntries = await new Promise<KnownEntry[]>((res, rej) => {
      client.listKnown({}, (err, resp) =>
        err ? rej(err) : res(resp.entries ?? []),
      );
    });

    const filtered = cfg.pinnedOrchEthAddress
      ? knownEntries.filter(
          (e) =>
            e.ethAddress.toLowerCase() ===
            cfg.pinnedOrchEthAddress!.toLowerCase(),
        )
      : knownEntries;

    const resolved = await Promise.allSettled(
      filtered.map(
        (entry) =>
          new Promise<ResolveResult>((res, rej) => {
            client.resolveByAddress(
              {
                ethAddress: entry.ethAddress,
                allowLegacyFallback: true,
                allowUnsigned: false,
                forceRefresh: false,
              },
              (err, resp) => (err ? rej(err) : res(resp)),
            );
          }),
      ),
    );

    const candidates: OrchCandidate[] = [];
    for (const r of resolved) {
      if (r.status !== "fulfilled") continue;
      for (const node of r.value.nodes ?? []) {
        if (!node.enabled) continue;
        for (const cap of node.capabilities ?? []) {
          if (cap.name !== cfg.capabilityId && !cap.name.startsWith("daydream-scope")) continue;
          for (const off of cap.offerings ?? []) {
            if (off.id !== cfg.offeringId) continue;
            candidates.push({
              brokerUrl: node.url,
              ethAddress: node.operatorAddress,
              capability: cap.name,
              offering: off.id,
              workUnit: cap.workUnit,
              pricePerWorkUnitWei: off.pricePerWorkUnitWei,
            });
          }
        }
      }
    }

    cache = { expiresAt: now + cfg.resolverSnapshotTtlMs, candidates };
    return candidates;
  }

  return {
    async list() {
      return load();
    },
    async pickRandom() {
      const candidates = await load();
      if (candidates.length === 0) {
        throw new Error(
          `no orchestrators advertising capability ${cfg.capabilityId}/${cfg.offeringId}`,
        );
      }
      const idx = Math.floor(Math.random() * candidates.length);
      return candidates[idx]!;
    },
  };
}

function newResolverClient(
  socketPath: string,
  protoRoot: string,
): ResolverClient {
  const def = protoLoader.loadSync(RESOLVER_PROTO_FILES, {
    keepCase: false,
    longs: String,
    enums: String,
    defaults: true,
    oneofs: true,
    includeDirs: [protoRoot],
  });
  const proto = grpc.loadPackageDefinition(def) as unknown as {
    livepeer: {
      registry: { v1: { Resolver: grpc.ServiceClientConstructor } };
    };
  };
  const ClientCtor = proto.livepeer.registry.v1.Resolver;
  return new ClientCtor(
    `unix:${socketPath}`,
    grpc.credentials.createInsecure(),
  ) as unknown as ResolverClient;
}
