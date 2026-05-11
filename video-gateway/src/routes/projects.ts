import type { FastifyInstance } from "fastify";
import { z } from "zod";

import type { Db } from "../db/pool.js";
import {
  createProject,
  deleteProject,
  getProjectById,
  listProjectsForCustomer,
  renameProject,
  summarizeProjectUsage,
  type ProjectRecord,
  type ProjectUsageSummary,
} from "../service/projects.js";

const CreateProjectBody = z.object({
  customer_id: z.string().min(1),
  name: z.string().trim().min(1).max(120),
});

const UpdateProjectBody = z.object({
  customer_id: z.string().min(1),
  name: z.string().trim().min(1).max(120),
});

const ProjectOwnershipQuery = z.object({
  customer_id: z.string().min(1),
});

const ListProjectsQuery = z.object({
  customer_id: z.string().min(1),
});

interface ProjectRouteService {
  list(customerId: string): Promise<ProjectRecord[]>;
  get(projectId: string): Promise<ProjectRecord | null>;
  create(input: { customerId: string; name: string }): Promise<ProjectRecord>;
  rename(input: { projectId: string; name: string }): Promise<ProjectRecord | null>;
  summarize(projectId: string): Promise<ProjectUsageSummary>;
  remove(projectId: string): Promise<boolean>;
}

export function registerProjects(
  app: FastifyInstance,
  deps: {
    videoDb?: Db;
    service?: ProjectRouteService;
  },
): void {
  const service = deps.service ?? (deps.videoDb ? createRouteService(deps.videoDb) : null);

  app.post("/v1/projects", async (req, reply) => {
    const parsed = CreateProjectBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.issues });
      return;
    }
    if (!service) {
      await reply.code(501).send({ error: "video_db_unavailable" });
      return;
    }
    const project = await service.create({
      customerId: parsed.data.customer_id,
      name: parsed.data.name,
    });
    await reply.code(201).send(serializeProject(project));
  });

  app.get("/v1/projects", async (req, reply) => {
    const parsed = ListProjectsQuery.safeParse(req.query);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_query", details: parsed.error.issues });
      return;
    }
    if (!service) {
      await reply.code(501).send({ error: "video_db_unavailable" });
      return;
    }
    const items = await service.list(parsed.data.customer_id);
    await reply.code(200).send({
      items: items.map(serializeProject),
    });
  });

  app.get("/v1/projects/:id", async (req, reply) => {
    const { id } = req.params as { id: string };
    const parsed = ProjectOwnershipQuery.safeParse(req.query);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_query", details: parsed.error.issues });
      return;
    }
    if (!service) {
      await reply.code(501).send({ error: "video_db_unavailable" });
      return;
    }
    const project = await requireOwnedProject(service, id, parsed.data.customer_id, reply);
    if (!project) return;
    const usage = await service.summarize(project.id);
    await reply.code(200).send({
      ...serializeProject(project),
      usage: serializeUsage(usage),
    });
  });

  app.patch("/v1/projects/:id", async (req, reply) => {
    const { id } = req.params as { id: string };
    const parsed = UpdateProjectBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.issues });
      return;
    }
    if (!service) {
      await reply.code(501).send({ error: "video_db_unavailable" });
      return;
    }
    const project = await requireOwnedProject(service, id, parsed.data.customer_id, reply);
    if (!project) return;
    const renamed = await service.rename({
      projectId: id,
      name: parsed.data.name,
    });
    if (!renamed) {
      await reply.code(404).send({ error: "project_not_found" });
      return;
    }
    const usage = await service.summarize(renamed.id);
    await reply.code(200).send({
      ...serializeProject(renamed),
      usage: serializeUsage(usage),
    });
  });

  app.delete("/v1/projects/:id", async (req, reply) => {
    const { id } = req.params as { id: string };
    const parsed = ProjectOwnershipQuery.safeParse(req.query);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_query", details: parsed.error.issues });
      return;
    }
    if (!service) {
      await reply.code(501).send({ error: "video_db_unavailable" });
      return;
    }
    const project = await requireOwnedProject(service, id, parsed.data.customer_id, reply);
    if (!project) return;
    const usage = await service.summarize(project.id);
    if (usage.assets > 0 || usage.uploads > 0 || usage.liveStreams > 0 || usage.webhooks > 0) {
      await reply.code(409).send({
        error: "project_not_empty",
        usage: serializeUsage(usage),
      });
      return;
    }
    const deleted = await service.remove(project.id);
    if (!deleted) {
      await reply.code(404).send({ error: "project_not_found" });
      return;
    }
    await reply.code(204).send();
  });
}

function createRouteService(videoDb: Db): ProjectRouteService {
  return {
    list(customerId) {
      return listProjectsForCustomer(videoDb, customerId);
    },
    get(projectId) {
      return getProjectById(videoDb, projectId);
    },
    create(input) {
      return createProject(videoDb, input);
    },
    rename(input) {
      return renameProject(videoDb, input);
    },
    summarize(projectId) {
      return summarizeProjectUsage(videoDb, projectId);
    },
    remove(projectId) {
      return deleteProject(videoDb, projectId);
    },
  };
}

async function requireOwnedProject(
  service: ProjectRouteService,
  projectId: string,
  customerId: string,
  reply: { code(statusCode: number): { send(payload: unknown): Promise<unknown> | unknown } },
): Promise<ProjectRecord | null> {
  const project = await service.get(projectId);
  if (!project || project.customerId !== customerId) {
    await reply.code(404).send({ error: "project_not_found" });
    return null;
  }
  return project;
}

function serializeProject(project: ProjectRecord): Record<string, unknown> {
  return {
    id: project.id,
    customer_id: project.customerId,
    name: project.name,
    created_at: project.createdAt.toISOString(),
  };
}

function serializeUsage(usage: ProjectUsageSummary): Record<string, number> {
  return {
    assets: usage.assets,
    uploads: usage.uploads,
    live_streams: usage.liveStreams,
    webhooks: usage.webhooks,
  };
}
