import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";

import type { FastifyRequest } from "fastify";

import type { Config } from "../config.js";
import { HEADER } from "../livepeer/headers.js";
import { normalizeCapabilityId } from "../livepeer/capabilityMap.js";

const RESOLVER_PROTO_FILES = [
  "livepeer/registry/v1/types.proto",
  "livepeer/registry/v1/resolver.proto",
];

export interface RouteCandidate {
  brokerUrl: string;
  capability: string;
  offering: string;
  model: string | null;
  ethAddress: string;
  pricePerWorkUnitWei: string;
  workUnit: string;
  extra: JsonValue | null;
  constraints: JsonValue | null;
}

export interface RouteSelectionInput {
  capability: string;
  offering: string;
  request: FastifyRequest;
}

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

interface ResolverProto {
  livepeer: { registry: { v1: { Resolver: grpc.ServiceClientConstructor } } };
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

type JsonPrimitive = string | number | boolean | null;
type JsonValue = JsonPrimitive | JsonValue[] | { [key: string]: JsonValue };

interface SelectionHints {
  preferredExtra: JsonValue | null;
  requiredConstraints: JsonValue | null;
  maxPricePerUnitWei: bigint | null;
}

interface PreferenceScore {
  fullMatch: boolean;
  matchedLeaves: number;
}

interface CachedSnapshot {
  expiresAt: number;
  candidates: RouteCandidate[];
}

export interface RouteSelector {
  select(input: RouteSelectionInput): Promise<RouteCandidate[]>;
  inspect(): Promise<RouteCandidate[]>;
}

export function createRouteSelector(cfg: Config): RouteSelector {
  if (!cfg.resolverSocket) {
    return {
      async select(input: RouteSelectionInput): Promise<RouteCandidate[]> {
        if (!cfg.brokerUrl) {
          throw new Error("static routing requested but LIVEPEER_BROKER_URL is unset");
        }
        return [
          {
            brokerUrl: cfg.brokerUrl,
            capability: input.capability,
            offering: input.offering,
            model: input.offering || null,
            ethAddress: "",
            pricePerWorkUnitWei: "0",
            workUnit: "",
            extra: null,
            constraints: null,
          },
        ];
      },
      async inspect(): Promise<RouteCandidate[]> {
        return [
          {
            brokerUrl: cfg.brokerUrl ?? '',
            capability: '',
            offering: '',
            model: null,
            ethAddress: '',
            pricePerWorkUnitWei: '0',
            workUnit: '',
            extra: null,
            constraints: null,
          },
        ];
      },
    };
  }

  const client = newResolverClient(cfg.resolverSocket, cfg.resolverProtoRoot);
  let cache: CachedSnapshot | null = null;

  return {
    async select(input: RouteSelectionInput): Promise<RouteCandidate[]> {
      const snapshot = await loadSnapshot(client, cfg, cache);
      cache = snapshot;

      const hints = readSelectionHints(input.request);
      const requestedModel = input.offering.trim();
      const matches = snapshot.candidates.filter((candidate) => {
        if (candidate.capability !== normalizeCapabilityId(input.capability)) return false;
        if (requestedModel) {
          if (candidate.model) {
            if (candidate.model !== requestedModel) return false;
          } else if (candidate.offering !== requestedModel) {
            return false;
          }
        }
        if (
          hints.maxPricePerUnitWei !== null &&
          safeBigInt(candidate.pricePerWorkUnitWei) > hints.maxPricePerUnitWei
        ) {
          return false;
        }
        if (
          hints.requiredConstraints !== null &&
          !isSubset(candidate.constraints, hints.requiredConstraints)
        ) {
          return false;
        }
        return true;
      });

      matches.sort((a, b) => compareCandidates(a, b, hints.preferredExtra));
      return matches;
    },
    async inspect(): Promise<RouteCandidate[]> {
      const snapshot = await loadSnapshot(client, cfg, cache);
      cache = snapshot;
      return snapshot.candidates;
    },
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

async function loadSnapshot(
  client: ResolverClient,
  cfg: Config,
  cached: CachedSnapshot | null,
): Promise<CachedSnapshot> {
  const now = Date.now();
  if (cached && cached.expiresAt > now) return cached;

  const known = await new Promise<KnownEntry[]>((resolve, reject) => {
    client.listKnown({}, (err, resp) => (err ? reject(err) : resolve(resp.entries ?? [])));
  });

  const resolved = await Promise.allSettled(
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

  return {
    expiresAt: now + cfg.resolverSnapshotTtlMs,
    candidates: collectResolvedResults(resolved).flatMap(flattenResolveResult),
  };
}

export function collectResolvedResults(
  results: PromiseSettledResult<ResolveResult>[],
): ResolveResult[] {
  return results.flatMap((result) => (result.status === "fulfilled" ? [result.value] : []));
}

function flattenResolveResult(resolved: ResolveResult): RouteCandidate[] {
  const out: RouteCandidate[] = [];
  for (const node of resolved.nodes ?? []) {
    if (!node.enabled || !node.url) continue;
    const nodeExtra = parseOpaqueJson(node.extraJson);
    for (const capability of node.capabilities ?? []) {
      const mergedExtra = mergeJsonObjects(nodeExtra, parseOpaqueJson(capability.extraJson));
      const model = inferModel(capability.name, mergedExtra);
      for (const offering of capability.offerings ?? []) {
        out.push({
          brokerUrl: node.url,
          capability: normalizeCapabilityId(stripCapabilityModelSuffix(capability.name)),
          offering: offering.id,
          model,
          ethAddress: node.operatorAddress,
          pricePerWorkUnitWei: offering.pricePerWorkUnitWei ?? "0",
          workUnit: capability.workUnit ?? "",
          extra: mergedExtra,
          constraints: parseOpaqueJson(offering.constraintsJson),
        });
      }
    }
  }
  return out;
}

function inferModel(capabilityName: string, extra: JsonValue | null): string | null {
  if (isJsonObject(extra) && isJsonObject(extra.openai)) {
    const model = extra.openai["model"];
    if (typeof model === "string" && model.trim().length > 0) return model.trim();
  }
  const suffix = capabilityModelSuffix(capabilityName);
  return suffix || null;
}

function stripCapabilityModelSuffix(capabilityName: string): string {
  const suffix = capabilityModelSuffix(capabilityName);
  if (!suffix) return capabilityName;
  return capabilityName.slice(0, -(suffix.length + 1));
}

function capabilityModelSuffix(capabilityName: string): string {
  for (const prefix of [
    "openai:chat-completions:",
    "openai:embeddings:",
    "openai:audio-transcriptions:",
    "openai:audio-speech:",
    "openai:images-generations:",
    "openai:realtime:",
  ]) {
    if (capabilityName.startsWith(prefix)) {
      return capabilityName.slice(prefix.length).trim();
    }
  }
  return "";
}

function readSelectionHints(req: FastifyRequest): SelectionHints {
  return {
    preferredExtra: parseJsonHeader(req.headers[HEADER.SELECTOR_EXTRA.toLowerCase()]),
    requiredConstraints: parseJsonHeader(req.headers[HEADER.SELECTOR_CONSTRAINTS.toLowerCase()]),
    maxPricePerUnitWei: parseBigIntHeader(req.headers[HEADER.SELECTOR_MAX_PRICE_WEI.toLowerCase()]),
  };
}

function parseJsonHeader(value: string | string[] | undefined): JsonValue | null {
  const raw = Array.isArray(value) ? value[0] : value;
  if (!raw) return null;
  return JSON.parse(raw) as JsonValue;
}

function parseBigIntHeader(value: string | string[] | undefined): bigint | null {
  const raw = Array.isArray(value) ? value[0] : value;
  if (!raw) return null;
  return BigInt(raw);
}

function parseOpaqueJson(raw: Buffer | Uint8Array | string | undefined): JsonValue | null {
  if (!raw) return null;
  const text =
    typeof raw === "string" ? raw : Buffer.from(raw).toString("utf8");
  if (!text) return null;
  return JSON.parse(text) as JsonValue;
}

function mergeJsonObjects(a: JsonValue | null, b: JsonValue | null): JsonValue | null {
  if (!isJsonObject(a)) return b;
  if (!isJsonObject(b)) return a;
  return { ...a, ...b };
}

function compareCandidates(a: RouteCandidate, b: RouteCandidate, preferredExtra: JsonValue | null): number {
  const scoreA = scorePreference(a.extra, preferredExtra);
  const scoreB = scorePreference(b.extra, preferredExtra);
  if (scoreA.fullMatch !== scoreB.fullMatch) return scoreA.fullMatch ? -1 : 1;
  if (scoreA.matchedLeaves !== scoreB.matchedLeaves) return scoreB.matchedLeaves - scoreA.matchedLeaves;

  const priceCmp = compareBigInts(a.pricePerWorkUnitWei, b.pricePerWorkUnitWei);
  if (priceCmp !== 0) return priceCmp;

  const urlCmp = a.brokerUrl.localeCompare(b.brokerUrl);
  if (urlCmp !== 0) return urlCmp;

  return a.ethAddress.localeCompare(b.ethAddress);
}

function scorePreference(candidate: JsonValue | null, preferred: JsonValue | null): PreferenceScore {
  if (preferred === null) return { fullMatch: true, matchedLeaves: 0 };
  const matchedLeaves = countMatchingLeaves(candidate, preferred);
  return {
    fullMatch: isSubset(candidate, preferred),
    matchedLeaves,
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
