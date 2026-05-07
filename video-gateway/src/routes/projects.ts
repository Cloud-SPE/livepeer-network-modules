import type { FastifyInstance } from "fastify";
import { z } from "zod";

const CreateProjectBody = z.object({
  name: z.string().min(1).max(120),
});

export function registerProjects(app: FastifyInstance): void {
  app.post("/v1/projects", async (req, reply) => {
    const parsed = CreateProjectBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.errors });
      return;
    }
    const id = `proj_${randomHex16()}`;
    await reply.code(201).send({ id, name: parsed.data.name });
  });

  app.get("/v1/projects", async (_req, reply) => {
    await reply.code(200).send({ items: [] });
  });
}

function randomHex16(): string {
  return Math.random().toString(16).slice(2, 18).padEnd(16, "0");
}
