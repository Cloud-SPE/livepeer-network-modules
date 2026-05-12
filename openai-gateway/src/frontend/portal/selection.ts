export interface CatalogVariantLike {
  selection_key: string;
  supportedModes: string[];
  extra?: unknown;
  constraints?: unknown;
  pricePerWorkUnitWei?: string;
}

export interface CatalogModelLike {
  supportedModes: string[];
  variants: CatalogVariantLike[];
}

export function modelSupportsInteractionMode(
  model: CatalogModelLike,
  interactionMode: string,
): boolean {
  return model.supportedModes.length === 0 || model.supportedModes.includes(interactionMode);
}

export function modelSupportsStream(model: CatalogModelLike): boolean {
  return modelSupportsInteractionMode(model, "http-stream@v0");
}

export function modelSupportsReqresp(model: CatalogModelLike): boolean {
  return modelSupportsInteractionMode(model, "http-reqresp@v0");
}

export function selectVariantForInteractionMode<T extends CatalogVariantLike>(
  variants: T[],
  selectedKey: string,
  interactionMode: string | null,
): T | null {
  if (variants.length === 0) return null;
  if (!interactionMode) {
    return variants.find((variant) => variant.selection_key === selectedKey) ?? variants[0] ?? null;
  }
  const selected = variants.find(
    (variant) =>
      variant.selection_key === selectedKey &&
      (variant.supportedModes.length === 0 || variant.supportedModes.includes(interactionMode)),
  );
  if (selected) return selected;
  return (
    variants.find(
      (variant) => variant.supportedModes.length === 0 || variant.supportedModes.includes(interactionMode),
    ) ?? null
  );
}

export function selectorHeadersForVariant(
  variant: CatalogVariantLike | null,
): Record<string, string> | undefined {
  if (!variant) return undefined;
  const headers: Record<string, string> = {};
  if (variant.constraints !== null && variant.constraints !== undefined) {
    headers["Livepeer-Selector-Constraints"] = JSON.stringify(variant.constraints);
  }
  if (variant.extra !== null && variant.extra !== undefined) {
    headers["Livepeer-Selector-Extra"] = JSON.stringify(variant.extra);
  }
  if (variant.pricePerWorkUnitWei) {
    headers["Livepeer-Selector-Max-Price-Wei"] = variant.pricePerWorkUnitWei;
  }
  return Object.keys(headers).length > 0 ? headers : undefined;
}
