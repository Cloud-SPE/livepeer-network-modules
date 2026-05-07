import type { Asset, EncodingTier, RenditionSpec } from "../types/index.js";
import { expandTier, type EncodingLadder } from "../config/encodingLadder.js";

export interface JobPlan {
  probe: { kind: "probe" };
  encodes: Array<{ kind: "encode"; rendition: RenditionSpec }>;
  thumbnail: { kind: "thumbnail" };
  finalize: { kind: "finalize" };
}

export interface PlanOpts {
  asset: Pick<Asset, "encodingTier">;
  ladder: EncodingLadder;
}

export function planJobs(opts: PlanOpts): JobPlan {
  const renditions = expandTier(opts.asset.encodingTier as EncodingTier, opts.ladder);
  return {
    probe: { kind: "probe" },
    encodes: renditions.map((r) => ({ kind: "encode" as const, rendition: r })),
    thumbnail: { kind: "thumbnail" },
    finalize: { kind: "finalize" },
  };
}
