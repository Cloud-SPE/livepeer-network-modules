import type { FastifyInstance } from "fastify";
import { z } from "zod";

const RegisterWebhookBody = z.object({
  url: z.string().url(),
  event_types: z.array(z.string()).optional(),
});

export function registerWebhooks(app: FastifyInstance): void {
  app.post("/v1/webhooks", async (req, reply) => {
    const parsed = RegisterWebhookBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.errors });
      return;
    }
    const id = `wh_${randomHex16()}`;
    const secret = `whsec_${randomHex16()}${randomHex16()}`;
    await reply.code(201).send({
      id,
      url: parsed.data.url,
      secret,
      event_types: parsed.data.event_types ?? null,
    });
  });

  app.get("/v1/webhooks", async (_req, reply) => {
    await reply.code(200).send({ items: [] });
  });
}

function randomHex16(): string {
  return Math.random().toString(16).slice(2, 18).padEnd(16, "0");
}
