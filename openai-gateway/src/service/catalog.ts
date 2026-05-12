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

export interface PortalCatalogVariant {
  selection_key: string;
  offering: string;
  supported_modes: string[];
  broker_url: string;
  eth_address: string;
  price_per_work_unit_wei: string;
  work_unit: string;
  extra: JsonValue | null;
  constraints: JsonValue | null;
}

export interface PortalCatalogModel {
  model_id: string;
  capability: string;
  supported_modes: string[];
  surface: CapabilitySurfaceDescriptor | null;
  variants: PortalCatalogVariant[];
}

export interface PortalCatalogCapability {
  id: string;
  label: string;
  models: PortalCatalogModel[];
}

export interface PortalCatalog {
  capabilities: PortalCatalogCapability[];
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

export function buildPortalModelCatalog(candidates: RouteCandidate[]): PortalCatalog {
  const grouped = new Map<string, Map<string, PortalCatalogModel>>();
  for (const candidate of candidates) {
    const modelId = candidate.model ?? candidate.offering;
    if (!modelId) continue;

    let byModel = grouped.get(candidate.capability);
    if (!byModel) {
      byModel = new Map<string, PortalCatalogModel>();
      grouped.set(candidate.capability, byModel);
    }

    let model = byModel.get(modelId);
    if (!model) {
      model = {
        model_id: modelId,
        capability: candidate.capability,
        supported_modes: [],
        surface: surfaceForCapability(candidate.capability),
        variants: [],
      };
      byModel.set(modelId, model);
    }

    const variant = {
      selection_key: buildSelectionKey(candidate.capability, modelId, candidate.offering, candidate.brokerUrl),
      offering: candidate.offering,
      supported_modes: candidate.interactionMode ? [candidate.interactionMode] : [],
      broker_url: candidate.brokerUrl,
      eth_address: candidate.ethAddress,
      price_per_work_unit_wei: candidate.pricePerWorkUnitWei,
      work_unit: candidate.workUnit,
      extra: candidate.extra,
      constraints: candidate.constraints,
    } satisfies PortalCatalogVariant;

    mergeVariant(model.variants, variant);
    mergeModes(model.supported_modes, variant.supported_modes);
  }

  const capabilities = [...grouped.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([capability, models]) => ({
      id: capability,
      label: capabilityLabel(capability),
      models: [...models.values()]
        .sort((a, b) => a.model_id.localeCompare(b.model_id))
        .map((model) => ({
          ...model,
          supported_modes: [...model.supported_modes].sort(),
          variants: [...model.variants].sort(compareVariants),
        })),
    }));

  return { capabilities };
}

function mergeVariant(variants: PortalCatalogVariant[], incoming: PortalCatalogVariant): void {
  const existing = variants.find((variant) => variant.selection_key === incoming.selection_key);
  if (!existing) {
    variants.push(incoming);
    return;
  }
  mergeModes(existing.supported_modes, incoming.supported_modes);
}

function mergeModes(target: string[], incoming: string[]): void {
  for (const mode of incoming) {
    if (!target.includes(mode)) target.push(mode);
  }
}

function buildSelectionKey(
  capability: string,
  modelId: string,
  offering: string,
  brokerUrl: string,
): string {
  return `${capability}|${modelId}|${offering}|${brokerUrl}`;
}

function compareVariants(a: PortalCatalogVariant, b: PortalCatalogVariant): number {
  if (a.offering !== b.offering) return a.offering.localeCompare(b.offering);
  if (a.broker_url !== b.broker_url) return a.broker_url.localeCompare(b.broker_url);
  return a.selection_key.localeCompare(b.selection_key);
}

function capabilityLabel(capability: string): string {
  switch (capability) {
    case "openai:chat-completions":
      return "Chat completions";
    case "openai:embeddings":
      return "Embeddings";
    case "openai:images-generations":
      return "Image generation";
    case "openai:audio-speech":
      return "Audio speech";
    case "openai:audio-transcriptions":
      return "Audio transcription";
    case "openai:realtime":
      return "Realtime";
    default:
      return capability;
  }
}
