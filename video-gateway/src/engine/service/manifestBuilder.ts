import type { Codec, RenditionSpec, Resolution } from "../types/index.js";

export interface ManifestRendition {
  codec: Codec;
  resolution: Resolution;
  bitrateKbps: number;
  variantUri: string;
}

const RESOLUTION_PIXELS: Record<Resolution, { w: number; h: number }> = {
  "240p": { w: 426, h: 240 },
  "360p": { w: 640, h: 360 },
  "480p": { w: 854, h: 480 },
  "720p": { w: 1280, h: 720 },
  "1080p": { w: 1920, h: 1080 },
};

const CODEC_STRING: Record<Codec, string> = {
  h264: "avc1.640028",
  hevc: "hvc1.1.6.L120.B0",
  av1: "av01.0.05M.08",
};

const AUDIO_STRING = "mp4a.40.2";

export function buildMasterManifest(renditions: ManifestRendition[]): string {
  const lines: string[] = [
    "#EXTM3U",
    "#EXT-X-VERSION:6",
    "#EXT-X-INDEPENDENT-SEGMENTS",
    "",
  ];
  for (const r of renditions) {
    const px = RESOLUTION_PIXELS[r.resolution];
    const bw = r.bitrateKbps * 1000;
    const codecStr = `${CODEC_STRING[r.codec]},${AUDIO_STRING}`;
    lines.push(
      `#EXT-X-STREAM-INF:BANDWIDTH=${bw},RESOLUTION=${px.w}x${px.h},CODECS="${codecStr}"`,
    );
    lines.push(r.variantUri);
  }
  return lines.join("\n") + "\n";
}

export function manifestRenditionsFromSpecs(
  specs: RenditionSpec[],
  variantUri: (s: RenditionSpec) => string,
): ManifestRendition[] {
  return specs.map((s) => ({
    codec: s.codec,
    resolution: s.resolution,
    bitrateKbps: s.bitrateKbps,
    variantUri: variantUri(s),
  }));
}
