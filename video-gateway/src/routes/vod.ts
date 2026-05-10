import type { FastifyInstance } from "fastify";
import { z } from "zod";
import type { VideoRouteSelector } from "../livepeer/routeSelector.js";
import { buildVodSelectionHints } from "../livepeer/selectionPolicy.js";

const VodSubmitBody = z.object({
  asset_id: z.string().min(1),
  encoding_tier: z.enum(["baseline", "standard", "premium"]).default("baseline"),
  offering: z.string().optional(),
});

const ListAssetsQuery = z.object({
  include_deleted: z.coerce.boolean().optional().default(false),
});

export interface VodDeps {
  routeSelector: VideoRouteSelector;
}

export function registerVod(app: FastifyInstance, deps: VodDeps): void {
  app.post("/v1/vod/submit", async (req, reply) => {
    const parsed = VodSubmitBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.issues });
      return;
    }
    const selectionHints = buildVodSelectionHints({
      encodingTier: parsed.data.encoding_tier,
    });
    const [route] = await deps.routeSelector.select({
      capability: "video:transcode.vod",
      offering: parsed.data.offering ?? "default",
      headers: req.headers,
      preferredExtra: selectionHints.preferredExtra,
      supportFilter: selectionHints.supportFilter,
    });
    if (!route) {
      await reply.code(503).send({
        error: "no_video_transcode_route",
        message: "no transcode route supports the requested VOD job",
      });
      return;
    }
    await reply.code(202).send({
      asset_id: parsed.data.asset_id,
      status: "queued",
      encoding_tier: parsed.data.encoding_tier,
      selected_offering: route.offering,
      selected_broker_url: route.brokerUrl,
    });
  });

  app.get("/v1/vod/:asset_id", async (req, reply) => {
    const { asset_id } = req.params as { asset_id: string };
    await reply.code(200).send({
      asset_id,
      status: "preparing",
      renditions: [],
    });
  });

  app.get("/v1/videos/assets", async (req, reply) => {
    const parsed = ListAssetsQuery.safeParse(req.query);
    const includeDeleted = parsed.success ? parsed.data.include_deleted : false;
    await reply.code(200).send({ items: [], include_deleted: includeDeleted });
  });

  app.get("/v1/videos/assets/:id", async (req, reply) => {
    const { id } = req.params as { id: string };
    const parsed = ListAssetsQuery.safeParse(req.query);
    const includeDeleted = parsed.success ? parsed.data.include_deleted : false;
    await reply
      .code(200)
      .send({ asset_id: id, status: "ready", include_deleted: includeDeleted });
  });

  app.delete("/v1/videos/assets/:id", async (req, reply) => {
    const { id } = req.params as { id: string };
    req.log.info({ asset_id: id }, "soft-delete asset");
    await reply.code(204).send();
  });
}
