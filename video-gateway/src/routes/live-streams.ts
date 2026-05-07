import type { FastifyInstance } from "fastify";
import { z } from "zod";

import type { Config } from "../config.js";

const CreateLiveStreamBody = z.object({
  project_id: z.string().min(1),
  recording_enabled: z.boolean().optional(),
  record_to_vod: z.boolean().optional(),
});

export interface LiveStreamsDeps {
  cfg: Config;
}

export function registerLiveStreams(app: FastifyInstance, deps: LiveStreamsDeps): void {
  app.post("/v1/live/streams", async (req, reply) => {
    const parsed = CreateLiveStreamBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.errors });
      return;
    }
    const streamId = `live_${randomHex16()}`;
    const streamKey = randomHex16();
    const recordToVod = parsed.data.record_to_vod ?? false;

    await reply.code(201).send({
      stream_id: streamId,
      rtmp_push_url: `rtmp://${deps.cfg.brokerRtmpHost}/live`,
      stream_key: streamKey,
      hls_playback_url: `${deps.cfg.hlsBaseUrl}/${streamId}/master.m3u8`,
      record_to_vod: recordToVod,
      expires_at: new Date(Date.now() + 24 * 3600 * 1000).toISOString(),
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
