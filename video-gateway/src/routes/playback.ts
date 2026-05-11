import type { FastifyInstance, FastifyRequest, FastifyReply } from "fastify";

import type { Config } from "../config.js";
import type { AssetRepo } from "../engine/index.js";
import type { StorageProvider } from "../engine/interfaces/storageProvider.js";
import type { LiveSessionDirectory } from "../livepeer/liveSessionDirectory.js";
import type { PlaybackIdRepo } from "../repo/playbackIds.js";

export interface PlaybackDeps {
  cfg: Config;
  liveSessions: LiveSessionDirectory;
  assetsRepo?: AssetRepo;
  playbackIds?: PlaybackIdRepo;
  storage?: StorageProvider;
}

export function registerPlayback(app: FastifyInstance, deps: PlaybackDeps): void {
  app.get("/v1/playback/:id", async (req, reply) => {
    const { id } = req.params as { id: string };
    if (deps.playbackIds && deps.storage) {
      const playback = await deps.playbackIds.byId(id);
      if (playback?.assetId) {
        const asset = deps.assetsRepo ? await deps.assetsRepo.byId(playback.assetId) : null;
        if (!asset) {
          await reply.code(404).send({ error: "playback_not_found" });
          return;
        }
        const manifestKey = deps.storage.pathFor({
          assetId: asset.id,
          kind: "manifest",
          filename: "master.m3u8",
        });
        const hlsUrl = await deps.storage.getSignedDownloadUrl({
          storageKey: manifestKey,
          expiresInSec: 3600,
        });
        await reply.code(200).send({
          playback_id: id,
          asset_id: asset.id,
          hls_url: hlsUrl,
        });
        return;
      }
    }
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
