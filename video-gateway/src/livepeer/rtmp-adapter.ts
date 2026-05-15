import type { Config } from "../config.js";
import { HEADER, SPEC_VERSION } from "./headers.js";
import { MODE } from "./capabilityMap.js";
import type { VideoRouteSelector } from "./routeSelector.js";
import { newRequestId } from "./requestId.js";
import type { DerivedSelectionHints } from "./selectionPolicy.js";

export interface OpenRtmpSessionInput {
  cfg: Config;
  routeSelector: VideoRouteSelector;
  callerId: string;
  offering: string;
  streamId: string;
  requestHeaders?: Record<string, string | string[] | undefined>;
  selectionHints?: DerivedSelectionHints;
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
  const routes = await input.routeSelector.select({
    capability: "video:live.rtmp",
    offering: input.offering,
    headers: input.requestHeaders,
    preferredExtra: input.selectionHints?.preferredExtra ?? null,
    supportFilter: input.selectionHints?.supportFilter,
  });
  if (routes.length === 0) {
    throw new Error("no video:live.rtmp route available");
  }
  let lastError: Error | null = null;
  for (const route of routes) {
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
    try {
      const res = await fetch(url, { method: "POST", headers, body });
      if (!res.ok) {
        await input.routeSelector.recordOutcome(
          route,
          { ok: false, retryable: res.status >= 500 || res.status === 429 },
          `live_session_open_failed:${res.status}`,
        );
        lastError = new Error(`live session-open failed: ${res.status}`);
        if (res.status >= 500 || res.status === 429) {
          continue;
        }
        break;
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
        await input.routeSelector.recordOutcome(
          route,
          { ok: false, retryable: true },
          "live_session_open_malformed_response",
        );
        lastError = new Error("live session-open returned malformed response");
        continue;
      }
      await input.routeSelector.recordOutcome(route, { ok: true, retryable: false });
      return {
        sessionId: payload.session_id,
        brokerRtmpUrl: payload.rtmp_ingest_url,
        hlsUrl: payload.hls_playback_url,
        expiresAt: payload.expires_at,
        requestId,
        brokerUrl: route.brokerUrl,
      };
    } catch (err) {
      await input.routeSelector.recordOutcome(
        route,
        { ok: false, retryable: true },
        err instanceof Error ? err.message : "live_session_open_exception",
      );
      lastError = err instanceof Error ? err : new Error("live session-open failed");
    }
  }
  throw lastError ?? new Error("live session-open failed");
}
