import type { RouteCandidate } from "./routeSelector.js";
import {
  surfaceForCapability,
  type CapabilitySurfaceDescriptor,
} from "./openaiSurface.js";

type JsonPrimitive = string | number | boolean | null;
type JsonValue = JsonPrimitive | JsonValue[] | { [key: string]: JsonValue };

export interface ModelCatalogEntry {
  id: string;
  capability: string;
  offering: string;
  supported_modes: string[];
  surface: CapabilitySurfaceDescriptor | null;
  brokerUrl: string;
  ethAddress: string;
  pricePerWorkUnitWei: string;
  workUnit: string;
  extra: JsonValue | null;
  constraints: JsonValue | null;
}

export function buildModelCatalog(candidates: RouteCandidate[]): ModelCatalogEntry[] {
  const grouped = new Map<string, ModelCatalogEntry>();
  for (const candidate of candidates) {
    const id = candidate.model ?? candidate.offering;
    if (!id) continue;
    const key = `${candidate.capability}|${id}|${candidate.offering}|${candidate.brokerUrl}`;
    const mode = candidate.interactionMode ?? "";
    const existing = grouped.get(key);
    if (existing) {
      if (mode && !existing.supported_modes.includes(mode)) {
        existing.supported_modes.push(mode);
        existing.supported_modes.sort();
      }
      continue;
    }
    grouped.set(key, {
      id,
      capability: candidate.capability,
      offering: candidate.offering,
      supported_modes: mode ? [mode] : [],
      surface: surfaceForCapability(candidate.capability),
      brokerUrl: candidate.brokerUrl,
      ethAddress: candidate.ethAddress,
      pricePerWorkUnitWei: candidate.pricePerWorkUnitWei,
      workUnit: candidate.workUnit,
      extra: candidate.extra,
      constraints: candidate.constraints,
    });
  }
  const out = [...grouped.values()];
  out.sort((a, b) => {
    if (a.capability !== b.capability) return a.capability.localeCompare(b.capability);
    if (a.id !== b.id) return a.id.localeCompare(b.id);
    return a.offering.localeCompare(b.offering);
  });
  return out;
}
