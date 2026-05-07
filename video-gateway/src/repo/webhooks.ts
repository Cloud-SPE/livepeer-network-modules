import { and, desc, eq, isNull } from "drizzle-orm";

import type { Db } from "../db/pool.js";
import { webhookDeliveries, webhookEndpoints } from "../db/schema.js";
import type {
  WebhookDelivery,
  WebhookEndpoint,
  WebhookEventType,
} from "../engine/index.js";

export interface WebhookEndpointRepo {
  insert(endpoint: WebhookEndpoint): Promise<WebhookEndpoint>;
  byId(id: string): Promise<WebhookEndpoint | null>;
  byProject(projectId: string): Promise<WebhookEndpoint[]>;
  disable(id: string, at: Date): Promise<void>;
}

export interface WebhookDeliveryRepo {
  insert(delivery: WebhookDelivery & { body: string; signatureHeader: string }): Promise<WebhookDelivery>;
  byId(id: string): Promise<WebhookDelivery | null>;
  byEndpoint(endpointId: string, opts: { limit: number }): Promise<WebhookDelivery[]>;
  markDelivered(id: string, attempts: number, at: Date): Promise<void>;
  markFailed(id: string, attempts: number, lastError: string): Promise<void>;
}

interface EndpointRow {
  id: string;
  projectId: string;
  url: string;
  secret: string;
  eventTypes: unknown;
  createdAt: Date;
  disabledAt: Date | null;
}

interface DeliveryRow {
  id: string;
  endpointId: string;
  eventType: string;
  body: string;
  status: string;
  attempts: number;
  lastError: string | null;
  createdAt: Date;
  deliveredAt: Date | null;
}

function rowToEndpoint(row: EndpointRow): WebhookEndpoint {
  const e: WebhookEndpoint = {
    id: row.id,
    projectId: row.projectId,
    url: row.url,
    secret: row.secret,
    eventTypes: (row.eventTypes as WebhookEventType[] | null) ?? null,
    createdAt: row.createdAt,
  };
  if (row.disabledAt !== null) e.disabledAt = row.disabledAt;
  return e;
}

function rowToDelivery(row: DeliveryRow): WebhookDelivery {
  const d: WebhookDelivery = {
    id: row.id,
    endpointId: row.endpointId,
    eventType: row.eventType as WebhookEventType,
    status: row.status as WebhookDelivery["status"],
    attempts: row.attempts,
    createdAt: row.createdAt,
  };
  if (row.lastError !== null) d.lastError = row.lastError;
  if (row.deliveredAt !== null) d.deliveredAt = row.deliveredAt;
  return d;
}

export function createWebhookEndpointRepo(db: Db): WebhookEndpointRepo {
  return {
    async insert(endpoint) {
      const [row] = await db
        .insert(webhookEndpoints)
        .values({
          id: endpoint.id,
          projectId: endpoint.projectId,
          url: endpoint.url,
          secret: endpoint.secret,
          eventTypes: endpoint.eventTypes ?? null,
          createdAt: endpoint.createdAt,
          disabledAt: endpoint.disabledAt ?? null,
        })
        .returning();
      if (!row) throw new Error("createWebhookEndpointRepo.insert: no row returned");
      return rowToEndpoint(row as EndpointRow);
    },

    async byId(id) {
      const rows = await db
        .select()
        .from(webhookEndpoints)
        .where(eq(webhookEndpoints.id, id))
        .limit(1);
      const row = rows[0];
      return row ? rowToEndpoint(row as EndpointRow) : null;
    },

    async byProject(projectId) {
      const rows = await db
        .select()
        .from(webhookEndpoints)
        .where(
          and(eq(webhookEndpoints.projectId, projectId), isNull(webhookEndpoints.disabledAt)),
        )
        .orderBy(desc(webhookEndpoints.createdAt));
      return rows.map((r) => rowToEndpoint(r as EndpointRow));
    },

    async disable(id, at) {
      await db.update(webhookEndpoints).set({ disabledAt: at }).where(eq(webhookEndpoints.id, id));
    },
  };
}

export function createWebhookDeliveryRepo(db: Db): WebhookDeliveryRepo {
  return {
    async insert(delivery) {
      const [row] = await db
        .insert(webhookDeliveries)
        .values({
          id: delivery.id,
          endpointId: delivery.endpointId,
          eventType: delivery.eventType,
          body: delivery.body,
          status: delivery.status,
          attempts: delivery.attempts,
          lastError: delivery.lastError ?? null,
          createdAt: delivery.createdAt,
          deliveredAt: delivery.deliveredAt ?? null,
        })
        .returning();
      if (!row) throw new Error("createWebhookDeliveryRepo.insert: no row returned");
      return rowToDelivery(row as DeliveryRow);
    },

    async byId(id) {
      const rows = await db
        .select()
        .from(webhookDeliveries)
        .where(eq(webhookDeliveries.id, id))
        .limit(1);
      const row = rows[0];
      return row ? rowToDelivery(row as DeliveryRow) : null;
    },

    async byEndpoint(endpointId, opts) {
      const rows = await db
        .select()
        .from(webhookDeliveries)
        .where(eq(webhookDeliveries.endpointId, endpointId))
        .orderBy(desc(webhookDeliveries.createdAt))
        .limit(opts.limit);
      return rows.map((r) => rowToDelivery(r as DeliveryRow));
    },

    async markDelivered(id, attempts, at) {
      await db
        .update(webhookDeliveries)
        .set({ status: "delivered", attempts, deliveredAt: at })
        .where(eq(webhookDeliveries.id, id));
    },

    async markFailed(id, attempts, lastError) {
      await db
        .update(webhookDeliveries)
        .set({ status: "failed", attempts, lastError })
        .where(eq(webhookDeliveries.id, id));
    },
  };
}
