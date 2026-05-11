import type { FastifyInstance } from "fastify";
import { z } from "zod";

import type { Db } from "../db/pool.js";
import { projects } from "../db/schema.js";
import { ensureDefaultProject, listProjectsForCustomer } from "../service/projects.js";

const CreateProjectBody = z.object({
  customer_id: z.string().min(1),
  name: z.string().min(1).max(120),
});

const ListProjectsQuery = z.object({
  customer_id: z.string().min(1),
});

export function registerProjects(app: FastifyInstance, deps: { videoDb?: Db }): void {
  app.post("/v1/projects", async (req, reply) => {
    const parsed = CreateProjectBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.issues });
      return;
    }
    if (!deps.videoDb) {
      await reply.code(501).send({ error: "video_db_unavailable" });
      return;
    }
    const existing = await listProjectsForCustomer(deps.videoDb, parsed.data.customer_id);
    if (parsed.data.name === "Default project" && existing.length === 0) {
      const project = await ensureDefaultProject(deps.videoDb, parsed.data.customer_id);
      await reply.code(201).send({
        id: project.id,
        customer_id: project.customerId,
        name: project.name,
        created_at: project.createdAt.toISOString(),
      });
      return;
    }
    const id = `proj_${randomHex16()}`;
    const [row] = await deps.videoDb
      .insert(projects)
      .values({
        id,
        customerId: parsed.data.customer_id,
        name: parsed.data.name,
        createdAt: new Date(),
      })
      .returning();
    await reply.code(201).send({
      id: row?.id ?? id,
      customer_id: row?.customerId ?? parsed.data.customer_id,
      name: row?.name ?? parsed.data.name,
      created_at: row?.createdAt?.toISOString?.() ?? new Date().toISOString(),
    });
  });

  app.get("/v1/projects", async (req, reply) => {
    const parsed = ListProjectsQuery.safeParse(req.query);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_query", details: parsed.error.issues });
      return;
    }
    if (!deps.videoDb) {
      await reply.code(501).send({ error: "video_db_unavailable" });
      return;
    }
    const items = await listProjectsForCustomer(deps.videoDb, parsed.data.customer_id);
    await reply.code(200).send({
      items: items.map((row) => ({
        id: row.id,
        customer_id: row.customerId,
        name: row.name,
        created_at: row.createdAt.toISOString(),
      })),
    });
  });
}

function randomHex16(): string {
  return Math.random().toString(16).slice(2, 18).padEnd(16, "0");
}
