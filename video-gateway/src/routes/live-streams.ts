import type { FastifyInstance } from "fastify";
import { z } from "zod";

import type { Config } from "../config.js";
import type { LiveSessionDirectory } from "../livepeer/liveSessionDirectory.js";
import { openRtmpSession } from "../livepeer/rtmp-adapter.js";
import type { VideoRouteSelector } from "../livepeer/routeSelector.js";
import { buildLiveSelectionHints } from "../livepeer/selectionPolicy.js";
import {
  selectAbrLadder,
  type CustomerTier,
} from "../service/abrSelector.js";

const CreateLiveStreamBody = z.object({
  project_id: z.string().min(1),
  recording_enabled: z.boolean().optional(),
  record_to_vod: z.boolean().optional(),
  offering: z.string().optional(),
  customer_tier: z.enum(["free", "prepaid", "enterprise"]).optional(),
});

export interface LiveStreamsDeps {
  cfg: Config;
  routeSelector: VideoRouteSelector;
  liveSessions: LiveSessionDirectory;
}

export function registerLiveStreams(app: FastifyInstance, deps: LiveStreamsDeps): void {
  app.post("/v1/live/streams", async (req, reply) => {
    const parsed = CreateLiveStreamBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.issues });
      return;
    }
    const streamId = `live_${randomHex16()}`;
    const recordToVod = parsed.data.record_to_vod ?? false;
    const customerTier: CustomerTier = parsed.data.customer_tier ?? "free";

    const ladder = selectAbrLadder({
      customerTier,
      policy: deps.cfg.abrPolicy,
    });
    const selectionHints = buildLiveSelectionHints({
      customerTier,
      recordToVod,
      ladder,
    });

    const session = await openRtmpSession({
      cfg: deps.cfg,
      routeSelector: deps.routeSelector,
      callerId: parsed.data.project_id,
      offering: parsed.data.offering ?? "default",
      streamId,
      requestHeaders: req.headers,
      selectionHints,
    });
    deps.liveSessions.record({
      sessionId: session.sessionId,
      brokerUrl: session.brokerUrl,
      hlsPlaybackUrl: session.hlsUrl,
    });

    await reply.code(201).send({
      stream_id: streamId,
      session_id: session.sessionId,
      rtmp_push_url: session.brokerRtmpUrl,
      stream_key: parseStreamKey(session.brokerRtmpUrl),
      hls_playback_url: session.hlsUrl,
      record_to_vod: recordToVod,
      customer_tier: customerTier,
      abr_ladder: ladder.map((r) => ({
        resolution: r.resolution,
        codec: r.codec,
        bitrate_kbps: r.bitrateKbps,
      })),
      expires_at: session.expiresAt,
      request_id: session.requestId,
    });
  });

  app.get("/v1/live/streams/:id", async (req, reply) => {
    const { id } = req.params as { id: string };
    await reply.code(200).send({
      stream_id: id,
      status: "starting",
      cost_accrued_cents: 0,
    });
  });

  app.post("/v1/live/streams/:id/end", async (req, reply) => {
    const { id } = req.params as { id: string };
    await reply.code(200).send({ stream_id: id, status: "ended" });
  });
}

function randomHex16(): string {
  return Math.random().toString(16).slice(2, 18).padEnd(16, "0");
}

function parseStreamKey(rtmpUrl: string): string {
  const segments = rtmpUrl.split("/");
  return segments[segments.length - 1] ?? "";
}
