import type { Capability } from "../engine/types/index.js";

export const MODE = {
  RTMP_INGRESS_HLS_EGRESS: "rtmp-ingress-hls-egress@v0",
  HTTP_REQRESP: "http-reqresp@v0",
  HTTP_STREAM: "http-stream@v0",
} as const;

export type Mode = (typeof MODE)[keyof typeof MODE];

export const CAPABILITY_MODES: Record<Capability, Mode> = {
  "video:transcode.abr": MODE.HTTP_REQRESP,
  "video:live.rtmp": MODE.RTMP_INGRESS_HLS_EGRESS,
};

export function modeForCapability(cap: Capability): Mode {
  return CAPABILITY_MODES[cap];
}
