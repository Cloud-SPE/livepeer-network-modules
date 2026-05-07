import type { Capability, CostQuote, RenditionSpec } from "../types/index.js";
import type { PricingConfig } from "../config/pricing.js";

export interface QuoteOpts {
  capability: Capability;
  callerTier: string;
  renditions: RenditionSpec[];
  estimatedSeconds: number | null;
  pricing: PricingConfig;
}

export function estimateCost(opts: QuoteOpts): CostQuote {
  const { capability, callerTier, renditions, estimatedSeconds, pricing } = opts;

  let cents = pricing.overheadCents;

  if (estimatedSeconds !== null) {
    for (const r of renditions) {
      const perSecond = pricing.vodCentsPerSecond[r.resolution]?.[r.codec] ?? 0;
      cents += perSecond * estimatedSeconds;
    }
  }

  return {
    cents: Math.ceil(cents),
    wei: 0n,
    estimatedSeconds,
    renditions: [...renditions],
    callerTier,
    capability,
  };
}

export interface UsageOpts {
  capability: Capability;
  renditions: RenditionSpec[];
  actualSeconds: number;
  pricing: PricingConfig;
}

export function reportUsage(opts: UsageOpts): {
  cents: number;
  wei: bigint;
  actualSeconds: number;
  renditions: RenditionSpec[];
  capability: Capability;
} {
  const { capability, renditions, actualSeconds, pricing } = opts;
  let cents = pricing.overheadCents;
  for (const r of renditions) {
    const perSecond = pricing.vodCentsPerSecond[r.resolution]?.[r.codec] ?? 0;
    cents += perSecond * actualSeconds;
  }
  return {
    cents: Math.ceil(cents),
    wei: 0n,
    actualSeconds,
    renditions: [...renditions],
    capability,
  };
}
