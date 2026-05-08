import type { FastifyInstance, FastifyRequest, FastifyReply } from "fastify";

import type { Config } from "../config.js";
import type { LiveSessionDirectory } from "../livepeer/liveSessionDirectory.js";

export interface PlaybackDeps {
  cfg: Config;
  liveSessions: LiveSessionDirectory;
}

export function registerPlayback(app: FastifyInstance, deps: PlaybackDeps): void {
  app.get("/v1/playback/:id", async (req, reply) => {
    const { id } = req.params as { id: string };
    const session = deps.liveSessions.get(id);
    await reply.code(200).send({
      playback_id: id,
      hls_url:
        session?.hlsPlaybackUrl ??
        `${(deps.cfg.hlsBaseUrl ?? "").replace(/\/$/, "")}/${id}/master.m3u8`,
    });
  });

  app.get("/_hls/*", async (req: FastifyRequest, reply: FastifyReply) => {
    const suffix = (req.params as { "*": string })["*"] ?? "";
    const sessionId = suffix.split("/", 1)[0] ?? "";
    const session = deps.liveSessions.get(sessionId);
    const baseUrl = session?.brokerUrl ?? deps.cfg.brokerUrl;
    if (!baseUrl) {
      await reply.code(404).send({ error: "playback_session_not_found" });
      return;
    }
    const upstream = `${baseUrl.replace(/\/$/, "")}/_hls/${suffix}`;
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
