import type { FastifyInstance } from "fastify";
import { z } from "zod";

const VodSubmitBody = z.object({
  asset_id: z.string().min(1),
  encoding_tier: z.enum(["baseline", "standard", "premium"]).default("baseline"),
});

export function registerVod(app: FastifyInstance): void {
  app.post("/v1/vod/submit", async (req, reply) => {
    const parsed = VodSubmitBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.errors });
      return;
    }
    await reply.code(202).send({
      asset_id: parsed.data.asset_id,
      status: "queued",
      encoding_tier: parsed.data.encoding_tier,
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

  app.get("/v1/videos/assets", async (_req, reply) => {
    await reply.code(200).send({ items: [] });
  });

  app.get("/v1/videos/assets/:id", async (req, reply) => {
    const { id } = req.params as { id: string };
    await reply.code(200).send({ asset_id: id, status: "ready" });
  });

  app.delete("/v1/videos/assets/:id", async (_req, reply) => {
    await reply.code(204).send();
  });
}
