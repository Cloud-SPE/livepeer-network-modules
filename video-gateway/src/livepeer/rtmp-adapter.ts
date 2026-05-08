import type { Config } from "../config.js";
import { HEADER, SPEC_VERSION } from "./headers.js";
import { MODE } from "./capabilityMap.js";
import type { VideoRouteSelector } from "./routeSelector.js";
import { newRequestId } from "./requestId.js";

export interface OpenRtmpSessionInput {
  cfg: Config;
  routeSelector: VideoRouteSelector;
  callerId: string;
  offering: string;
  streamId: string;
  requestHeaders?: Record<string, string | string[] | undefined>;
}

export interface OpenRtmpSessionOutput {
  sessionId: string;
  brokerRtmpUrl: string;
  hlsUrl: string;
  expiresAt: string;
  requestId: string;
  brokerUrl: string;
}

export async function openRtmpSession(
  input: OpenRtmpSessionInput,
): Promise<OpenRtmpSessionOutput> {
  const requestId = newRequestId();
  const [route] = await input.routeSelector.select({
    capability: "video:live.rtmp",
    offering: input.offering,
    headers: input.requestHeaders,
  });
  if (!route) {
    throw new Error("no video:live.rtmp route available");
  }
  const url = `${route.brokerUrl.replace(/\/$/, "")}/v1/cap`;
  const headers: Record<string, string> = {
    [HEADER.CAPABILITY]: "video:live.rtmp",
    [HEADER.OFFERING]: input.offering,
    [HEADER.MODE]: MODE.RTMP_INGRESS_HLS_EGRESS,
    [HEADER.SPEC_VERSION]: SPEC_VERSION,
    [HEADER.REQUEST_ID]: requestId,
    "Content-Type": "application/json",
  };
  const body = JSON.stringify({
    caller_id: input.callerId,
    stream_id: input.streamId,
  });
  const res = await fetch(url, { method: "POST", headers, body });
  if (!res.ok) {
    throw new Error(`live session-open failed: ${res.status}`);
  }
  const payload = (await res.json()) as {
    session_id?: string;
    rtmp_ingest_url?: string;
    hls_playback_url?: string;
    expires_at?: string;
  };
  if (
    !payload.session_id ||
    !payload.rtmp_ingest_url ||
    !payload.hls_playback_url ||
    !payload.expires_at
  ) {
    throw new Error("live session-open returned malformed response");
  }
  return {
    sessionId: payload.session_id,
    brokerRtmpUrl: payload.rtmp_ingest_url,
    hlsUrl: payload.hls_playback_url,
    expiresAt: payload.expires_at,
    requestId,
    brokerUrl: route.brokerUrl,
  };
}
