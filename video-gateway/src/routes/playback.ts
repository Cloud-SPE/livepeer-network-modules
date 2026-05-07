import type { FastifyInstance, FastifyRequest, FastifyReply } from "fastify";

import type { Config } from "../config.js";

export interface PlaybackDeps {
  cfg: Config;
}

export function registerPlayback(app: FastifyInstance, deps: PlaybackDeps): void {
  app.get("/v1/playback/:id", async (req, reply) => {
    const { id } = req.params as { id: string };
    await reply.code(200).send({
      playback_id: id,
      hls_url: `${deps.cfg.hlsBaseUrl}/${id}/master.m3u8`,
    });
  });

  app.get("/_hls/*", async (req: FastifyRequest, reply: FastifyReply) => {
    const upstream = `${deps.cfg.brokerUrl.replace(/\/$/, "")}/_hls${(req.params as { "*": string })["*"] ?? ""}`;
    try {
      const res = await fetch(upstream, { method: "GET" });
      const buf = Buffer.from(await res.arrayBuffer());
      reply.code(res.status);
      res.headers.forEach((v, k) => {
        reply.header(k, v);
      });
      await reply.send(buf);
    } catch (err) {
      req.log.error({ err, upstream }, "playback proxy failure");
      await reply.code(502).send({ error: "upstream_unavailable" });
    }
  });
}
