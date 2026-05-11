import { randomBytes } from "node:crypto";

import { desc, inArray } from "drizzle-orm";
import type { FastifyInstance } from "fastify";
import { z } from "zod";

import type { Db } from "../db/pool.js";
import { webhookDeliveries } from "../db/schema.js";
import type { WebhookDeliveryRepo, WebhookEndpointRepo } from "../repo/index.js";
import type { WebhookEventType } from "../engine/types/webhook.js";
import { getProjectById } from "../service/projects.js";

const RegisterWebhookBody = z.object({
  project_id: z.string().min(1),
  url: z.string().url(),
  event_types: z.array(z.string()).optional(),
});

const ListWebhooksQuery = z.object({
  project_id: z.string().min(1),
});

const WebhookProjectBody = z.object({
  project_id: z.string().min(1),
});

export function registerWebhooks(
  app: FastifyInstance,
  deps: {
    videoDb?: Db;
    endpoints?: WebhookEndpointRepo;
    deliveries?: WebhookDeliveryRepo;
  } = {},
): void {
  app.post("/v1/webhooks", async (req, reply) => {
    const parsed = RegisterWebhookBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.issues });
      return;
    }
    if (!deps.videoDb || !deps.endpoints) {
      await reply.code(501).send({ error: "webhook_registry_unavailable" });
      return;
    }
    const project = await getProjectById(deps.videoDb, parsed.data.project_id);
    if (!project) {
      await reply.code(404).send({ error: "project_not_found" });
      return;
    }
    const id = `wh_${randomHex16()}`;
    const secret = newWebhookSecret();
    const endpoint = await deps.endpoints.insert({
      id,
      projectId: project.id,
      url: parsed.data.url,
      secret,
      eventTypes: normalizeEventTypes(parsed.data.event_types),
      createdAt: new Date(),
    });
    await reply.code(201).send(serializeEndpoint(endpoint, null));
  });

  app.get("/v1/webhooks", async (req, reply) => {
    const parsed = ListWebhooksQuery.safeParse(req.query);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_query", details: parsed.error.issues });
      return;
    }
    if (!deps.videoDb || !deps.endpoints) {
      await reply.code(501).send({ error: "webhook_registry_unavailable" });
      return;
    }
    const project = await getProjectById(deps.videoDb, parsed.data.project_id);
    if (!project) {
      await reply.code(404).send({ error: "project_not_found" });
      return;
    }
    const endpoints = await deps.endpoints.byProject(project.id);
    const deliveryRows = deps.videoDb
      ? await recentDeliveriesByEndpoint(deps.videoDb, endpoints.map((row) => row.id))
      : new Map<string, { status: number | null; deliveredAt: string | null }>();
    await reply.code(200).send({
      items: endpoints.map((row) => serializeEndpoint(row, deliveryRows.get(row.id) ?? null)),
    });
  });

  app.get("/v1/webhook-deliveries", async (req, reply) => {
    const parsed = ListWebhooksQuery.safeParse(req.query);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_query", details: parsed.error.issues });
      return;
    }
    if (!deps.videoDb || !deps.endpoints) {
      await reply.code(501).send({ error: "webhook_registry_unavailable" });
      return;
    }
    const project = await getProjectById(deps.videoDb, parsed.data.project_id);
    if (!project) {
      await reply.code(404).send({ error: "project_not_found" });
      return;
    }
    const endpoints = await deps.endpoints.byProject(project.id);
    const endpointIds = endpoints.map((row) => row.id);
    if (endpointIds.length === 0) {
      await reply.code(200).send({ items: [] });
      return;
    }
    const rows = await deps.videoDb
      .select()
      .from(webhookDeliveries)
      .where(inArray(webhookDeliveries.endpointId, endpointIds))
      .orderBy(desc(webhookDeliveries.createdAt))
      .limit(100);
    await reply.code(200).send({
      items: rows.map((row) => ({
        id: row.id,
        endpoint_id: row.endpointId,
        event_type: row.eventType,
        status:
          row.status === "delivered"
            ? 200
            : row.status === "failed"
              ? 500
              : null,
        attempts: row.attempts,
        delivered_at: row.deliveredAt?.toISOString() ?? null,
      })),
    });
  });

  app.post("/v1/webhooks/:id/rotate", async (req, reply) => {
    const parsed = WebhookProjectBody.safeParse(req.body);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_body", details: parsed.error.issues });
      return;
    }
    if (!deps.videoDb || !deps.endpoints) {
      await reply.code(501).send({ error: "webhook_registry_unavailable" });
      return;
    }
    const project = await getProjectById(deps.videoDb, parsed.data.project_id);
    if (!project) {
      await reply.code(404).send({ error: "project_not_found" });
      return;
    }
    const endpoint = await deps.endpoints.byId((req.params as { id: string }).id);
    if (!endpoint || endpoint.projectId !== project.id) {
      await reply.code(404).send({ error: "webhook_not_found" });
      return;
    }
    const secret = newWebhookSecret();
    await deps.endpoints.updateSecret(endpoint.id, secret);
    await reply.code(200).send({
      ...serializeEndpoint({ ...endpoint, secret }, null),
    });
  });

  app.delete("/v1/webhooks/:id", async (req, reply) => {
    const parsed = ListWebhooksQuery.safeParse(req.query);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_query", details: parsed.error.issues });
      return;
    }
    if (!deps.videoDb || !deps.endpoints) {
      await reply.code(501).send({ error: "webhook_registry_unavailable" });
      return;
    }
    const project = await getProjectById(deps.videoDb, parsed.data.project_id);
    if (!project) {
      await reply.code(404).send({ error: "project_not_found" });
      return;
    }
    const endpoint = await deps.endpoints.byId((req.params as { id: string }).id);
    if (!endpoint || endpoint.projectId !== project.id) {
      await reply.code(404).send({ error: "webhook_not_found" });
      return;
    }
    await deps.endpoints.disable(endpoint.id, new Date());
    await reply.code(204).send();
  });
}

async function recentDeliveriesByEndpoint(
  videoDb: Db,
  endpointIds: string[],
): Promise<Map<string, { status: number | null; deliveredAt: string | null }>> {
  if (endpointIds.length === 0) return new Map();
  const rows = await videoDb
    .select()
    .from(webhookDeliveries)
    .where(inArray(webhookDeliveries.endpointId, endpointIds))
    .orderBy(desc(webhookDeliveries.createdAt));
  const byEndpoint = new Map<string, { status: number | null; deliveredAt: string | null }>();
  for (const row of rows) {
    if (byEndpoint.has(row.endpointId)) continue;
    byEndpoint.set(row.endpointId, {
      status:
        row.status === "delivered"
          ? 200
          : row.status === "failed"
            ? 500
            : null,
      deliveredAt: row.deliveredAt?.toISOString() ?? null,
    });
  }
  return byEndpoint;
}

function serializeEndpoint(
  row: {
    id: string;
    projectId: string;
    url: string;
    secret: string;
    eventTypes: WebhookEventType[] | null;
    createdAt: Date;
  },
  delivery: { status: number | null; deliveredAt: string | null } | null,
): Record<string, unknown> {
  return {
    id: row.id,
    project_id: row.projectId,
    url: row.url,
    secret: row.secret,
    signingSecret: row.secret,
    event_types: row.eventTypes,
    events: row.eventTypes,
    created_at: row.createdAt.toISOString(),
    createdAt: row.createdAt.toISOString(),
    last_delivery_status: delivery?.status ?? null,
    last_delivery_at: delivery?.deliveredAt ?? null,
    lastDeliveryStatus: delivery?.status ?? null,
    lastDeliveryAt: delivery?.deliveredAt ?? null,
  };
}

function normalizeEventTypes(input: string[] | undefined): WebhookEventType[] | null {
  if (!input || input.length === 0) return null;
  return input.map((value) => normalizeEventType(value));
}

function normalizeEventType(value: string): WebhookEventType {
  const normalized = value.trim();
  if (normalized.startsWith("video.")) return normalized as WebhookEventType;
  switch (normalized) {
    case "asset.ready":
      return "video.asset.ready";
    case "asset.errored":
      return "video.asset.errored";
    case "stream.started":
      return "video.live_stream.active";
    case "stream.ended":
      return "video.live_stream.ended";
    case "recording.ready":
      return "video.live_stream.recording_ready";
    default:
      return normalized as WebhookEventType;
  }
}

function newWebhookSecret(): string {
  return `whsec_${randomBytes(16).toString("hex")}`;
}

function randomHex16(): string {
  return Math.random().toString(16).slice(2, 18).padEnd(16, "0");
}
