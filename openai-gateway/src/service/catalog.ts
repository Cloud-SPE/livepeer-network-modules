import type { RouteCandidate } from "./routeSelector.js";

type JsonPrimitive = string | number | boolean | null;
type JsonValue = JsonPrimitive | JsonValue[] | { [key: string]: JsonValue };

export interface ModelCatalogEntry {
  id: string;
  capability: string;
  offering: string;
  brokerUrl: string;
  ethAddress: string;
  pricePerWorkUnitWei: string;
  workUnit: string;
  extra: JsonValue | null;
  constraints: JsonValue | null;
}

export function buildModelCatalog(candidates: RouteCandidate[]): ModelCatalogEntry[] {
  const seen = new Set<string>();
  const out: ModelCatalogEntry[] = [];
  for (const candidate of candidates) {
    const id = candidate.model ?? candidate.offering;
    if (!id) continue;
    const key = `${candidate.capability}|${id}|${candidate.offering}|${candidate.brokerUrl}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({
      id,
      capability: candidate.capability,
      offering: candidate.offering,
      brokerUrl: candidate.brokerUrl,
      ethAddress: candidate.ethAddress,
      pricePerWorkUnitWei: candidate.pricePerWorkUnitWei,
      workUnit: candidate.workUnit,
      extra: candidate.extra,
      constraints: candidate.constraints,
    });
  }
  out.sort((a, b) => {
    if (a.capability !== b.capability) return a.capability.localeCompare(b.capability);
    if (a.id !== b.id) return a.id.localeCompare(b.id);
    return a.offering.localeCompare(b.offering);
  });
  return out;
}
