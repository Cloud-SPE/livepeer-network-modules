import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";

const RESOLVER_PROTO_FILES = [
  "livepeer/registry/v1/types.proto",
  "livepeer/registry/v1/resolver.proto",
];

export const SELECTOR_HEADER = {
  EXTRA: "livepeer-selector-extra",
  CONSTRAINTS: "livepeer-selector-constraints",
  MAX_PRICE_WEI: "livepeer-selector-max-price-wei",
} as const;

type JsonPrimitive = string | number | boolean | null;
type JsonValue = JsonPrimitive | JsonValue[] | { [key: string]: JsonValue };

export interface NodeDescriptor {
  nodeId: string;
  nodeUrl: string;
  ethAddress: string;
  capabilities: readonly string[];
  offering?: string;
  extra?: JsonValue | null;
  constraints?: JsonValue | null;
  pricePerWorkUnitWei?: string;
}

export interface SelectNodeRequest {
  capability: string;
  offering: string;
  extra?: JsonValue | null;
  constraints?: JsonValue | null;
  maxPricePerUnitWei?: string | null;
}

export interface ServiceRegistryClient {
  listVtuberNodes(): Promise<readonly NodeDescriptor[]>;
  getNode(nodeId: string): Promise<NodeDescriptor | null>;
  select(req: SelectNodeRequest): Promise<NodeDescriptor | null>;
  close(): Promise<void>;
}

interface ResolverClient extends grpc.Client {
  listKnown(
    req: Record<string, never>,
    cb: (err: grpc.ServiceError | null, resp: { entries: KnownEntry[] }) => void,
  ): void;
  resolveByAddress(
    req: ResolveByAddressRequest,
    cb: (err: grpc.ServiceError | null, resp: ResolveResult) => void,
  ): void;
}

interface ResolverProto {
  livepeer: { registry: { v1: { Resolver: grpc.ServiceClientConstructor } } };
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
  extraJson?: Buffer | Uint8Array | string;
  capabilities: ResolverCapability[];
}

interface ResolverCapability {
  name: string;
  workUnit: string;
  extraJson?: Buffer | Uint8Array | string;
  offerings: ResolverOffering[];
}

interface ResolverOffering {
  id: string;
  pricePerWorkUnitWei: string;
  constraintsJson?: Buffer | Uint8Array | string;
}

interface CachedSnapshot {
  expiresAt: number;
  nodes: NodeDescriptor[];
}

export function createServiceRegistryClient(input: {
  brokerUrl: string | null;
  resolverSocket: string | null;
  resolverProtoRoot: string;
  resolverSnapshotTtlMs: number;
}): ServiceRegistryClient {
  if (!input.resolverSocket) {
    if (!input.brokerUrl) {
      throw new Error(
        "static routing requested but neither LIVEPEER_BROKER_URL nor LIVEPEER_RESOLVER_SOCKET is set",
      );
    }
    const node = staticNodeDescriptor(input.brokerUrl);
    return {
      async listVtuberNodes(): Promise<readonly NodeDescriptor[]> {
        return [node];
      },
      async getNode(nodeId: string): Promise<NodeDescriptor | null> {
        return node.nodeId === nodeId ? node : null;
      },
      async select(): Promise<NodeDescriptor | null> {
        return node;
      },
      async close(): Promise<void> {},
    };
  }

  const client = newResolverClient(input.resolverSocket, input.resolverProtoRoot);
  let cache: CachedSnapshot | null = null;

  async function loadSnapshot(): Promise<CachedSnapshot> {
    const now = Date.now();
    if (cache && cache.expiresAt > now) return cache;

    const known = await new Promise<KnownEntry[]>((resolve, reject) => {
      client.listKnown({}, (err, resp) => (err ? reject(err) : resolve(resp.entries ?? [])));
    });

    const resolved = await Promise.all(
      known.map(
        (entry) =>
          new Promise<ResolveResult>((resolve, reject) => {
            client.resolveByAddress(
              {
                ethAddress: entry.ethAddress,
                allowLegacyFallback: true,
                allowUnsigned: false,
                forceRefresh: false,
              },
              (err, resp) => (err ? reject(err) : resolve(resp)),
            );
          }),
      ),
    );

    cache = {
      expiresAt: now + input.resolverSnapshotTtlMs,
      nodes: resolved.flatMap(flattenResolveResult),
    };
    return cache;
  }

  return {
    async listVtuberNodes(): Promise<readonly NodeDescriptor[]> {
      return (await loadSnapshot()).nodes;
    },
    async getNode(nodeId: string): Promise<NodeDescriptor | null> {
      return (await loadSnapshot()).nodes.find((node) => node.nodeId === nodeId) ?? null;
    },
    async select(req: SelectNodeRequest): Promise<NodeDescriptor | null> {
      const matches = (await loadSnapshot()).nodes.filter((node) => {
        if (!node.capabilities.includes(req.capability)) return false;
        if (node.offering !== undefined && node.offering !== req.offering) return false;
        if (
          req.maxPricePerUnitWei &&
          safeBigInt(node.pricePerWorkUnitWei ?? "0") > safeBigInt(req.maxPricePerUnitWei)
        ) {
          return false;
        }
        if (req.constraints && !isSubset(node.constraints ?? null, req.constraints)) {
          return false;
        }
        return true;
      });
      matches.sort((a, b) => compareCandidates(a, b, req.extra ?? null));
      return matches[0] ?? null;
    },
    async close(): Promise<void> {
      client.close();
    },
  };
}

function staticNodeDescriptor(brokerUrl: string): NodeDescriptor {
  return {
    nodeId: brokerUrl,
    nodeUrl: brokerUrl,
    ethAddress: "",
    capabilities: ["livepeer:vtuber-session"],
    offering: "default",
    extra: null,
    constraints: null,
    pricePerWorkUnitWei: "0",
  };
}

function newResolverClient(socketPath: string, protoRoot: string): ResolverClient {
  const def = protoLoader.loadSync(RESOLVER_PROTO_FILES, {
    keepCase: false,
    longs: String,
    enums: String,
    defaults: true,
    oneofs: true,
    includeDirs: [protoRoot],
  });
  const proto = grpc.loadPackageDefinition(def) as unknown as ResolverProto;
  const ClientCtor = proto.livepeer.registry.v1.Resolver;
  return new ClientCtor(
    `unix:${socketPath}`,
    grpc.credentials.createInsecure(),
  ) as unknown as ResolverClient;
}

function flattenResolveResult(resolved: ResolveResult): NodeDescriptor[] {
  const out: NodeDescriptor[] = [];
  for (const node of resolved.nodes ?? []) {
    if (!node.enabled || !node.url) continue;
    const nodeExtra = parseOpaqueJson(node.extraJson);
    for (const capability of node.capabilities ?? []) {
      const mergedExtra = mergeJsonObjects(nodeExtra, parseOpaqueJson(capability.extraJson));
      for (const offering of capability.offerings ?? []) {
        out.push({
          nodeId: `${node.operatorAddress}@${node.url}`,
          nodeUrl: node.url,
          ethAddress: node.operatorAddress,
          capabilities: [capability.name],
          offering: offering.id,
          extra: mergedExtra,
          constraints: parseOpaqueJson(offering.constraintsJson),
          pricePerWorkUnitWei: offering.pricePerWorkUnitWei ?? "0",
        });
      }
    }
  }
  return out;
}

function parseOpaqueJson(raw: Buffer | Uint8Array | string | undefined): JsonValue | null {
  if (!raw) return null;
  const text = typeof raw === "string" ? raw : Buffer.from(raw).toString("utf8");
  if (!text) return null;
  return JSON.parse(text) as JsonValue;
}

function mergeJsonObjects(a: JsonValue | null, b: JsonValue | null): JsonValue | null {
  if (!isJsonObject(a)) return b;
  if (!isJsonObject(b)) return a;
  return { ...a, ...b };
}

function compareCandidates(a: NodeDescriptor, b: NodeDescriptor, preferredExtra: JsonValue | null): number {
  const scoreA = scorePreference(a.extra ?? null, preferredExtra);
  const scoreB = scorePreference(b.extra ?? null, preferredExtra);
  if (scoreA.fullMatch !== scoreB.fullMatch) return scoreA.fullMatch ? -1 : 1;
  if (scoreA.matchedLeaves !== scoreB.matchedLeaves) {
    return scoreB.matchedLeaves - scoreA.matchedLeaves;
  }
  const priceCmp = compareBigInts(a.pricePerWorkUnitWei ?? "0", b.pricePerWorkUnitWei ?? "0");
  if (priceCmp !== 0) return priceCmp;
  const urlCmp = a.nodeUrl.localeCompare(b.nodeUrl);
  if (urlCmp !== 0) return urlCmp;
  return a.ethAddress.localeCompare(b.ethAddress);
}

function scorePreference(candidate: JsonValue | null, preferred: JsonValue | null): {
  fullMatch: boolean;
  matchedLeaves: number;
} {
  if (preferred === null) return { fullMatch: true, matchedLeaves: 0 };
  return {
    fullMatch: isSubset(candidate, preferred),
    matchedLeaves: countMatchingLeaves(candidate, preferred),
  };
}

function countMatchingLeaves(candidate: JsonValue | null, preferred: JsonValue): number {
  if (preferred === null || typeof preferred !== "object") {
    return deepEqual(candidate, preferred) ? 1 : 0;
  }
  if (Array.isArray(preferred)) {
    if (!Array.isArray(candidate)) return 0;
    return preferred.reduce<number>((sum, value) => {
      const found = candidate.some((candidateValue) => deepEqual(candidateValue, value));
      return sum + (found ? 1 : 0);
    }, 0);
  }
  if (!isJsonObject(candidate)) return 0;

  let matches = 0;
  for (const [key, value] of Object.entries(preferred)) {
    matches += countMatchingLeaves(candidate[key] ?? null, value);
  }
  return matches;
}

function isSubset(candidate: JsonValue | null, required: JsonValue): boolean {
  if (required === null || typeof required !== "object") {
    return deepEqual(candidate, required);
  }
  if (Array.isArray(required)) {
    if (!Array.isArray(candidate)) return false;
    return required.every((requiredValue) =>
      candidate.some((candidateValue) => deepEqual(candidateValue, requiredValue)),
    );
  }
  if (!isJsonObject(candidate)) return false;
  return Object.entries(required).every(([key, value]) => isSubset(candidate[key] ?? null, value));
}

function isJsonObject(value: JsonValue | null): value is { [key: string]: JsonValue } {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function deepEqual(a: JsonValue | null, b: JsonValue | null): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b) return false;
  if (Array.isArray(a) && Array.isArray(b)) {
    if (a.length !== b.length) return false;
    return a.every((value, idx) => deepEqual(value, b[idx] ?? null));
  }
  if (isJsonObject(a) && isJsonObject(b)) {
    const keysA = Object.keys(a).sort();
    const keysB = Object.keys(b).sort();
    if (keysA.length !== keysB.length) return false;
    return keysA.every((key, idx) => key === keysB[idx] && deepEqual(a[key], b[key]));
  }
  return false;
}

function safeBigInt(value: string): bigint {
  try {
    return BigInt(value);
  } catch {
    return 0n;
  }
}

function compareBigInts(a: string, b: string): number {
  const av = safeBigInt(a);
  const bv = safeBigInt(b);
  if (av < bv) return -1;
  if (av > bv) return 1;
  return 0;
}
