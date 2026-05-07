import type { Config } from "../config.js";
import { HEADER, SPEC_VERSION } from "./headers.js";
import { MODE } from "./capabilityMap.js";
import { newRequestId } from "./requestId.js";

export interface OpenRtmpSessionInput {
  cfg: Config;
  callerId: string;
  offering: string;
  streamId: string;
}

export interface OpenRtmpSessionOutput {
  sessionId: string;
  brokerRtmpUrl: string;
  hlsUrl: string;
  expiresAt: string;
  requestId: string;
}

export async function openRtmpSession(
  input: OpenRtmpSessionInput,
): Promise<OpenRtmpSessionOutput> {
  const requestId = newRequestId();
  const sessionId = `sess_${requestId.slice(4)}`;
  const url = `${input.cfg.brokerUrl.replace(/\/$/, "")}/v1/cap`;
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
  try {
    await fetch(url, { method: "POST", headers, body });
  } catch {
    /* broker dial best-effort in this scaffold; real wire impl handles backoff */
  }
  return {
    sessionId,
    brokerRtmpUrl: `rtmp://${input.cfg.brokerRtmpHost}/${sessionId}`,
    hlsUrl: `${input.cfg.hlsBaseUrl}/${sessionId}/master.m3u8`,
    expiresAt: new Date(Date.now() + 24 * 3600 * 1000).toISOString(),
    requestId,
  };
}
