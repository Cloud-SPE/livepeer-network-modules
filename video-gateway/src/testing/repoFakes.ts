import type {
  Asset,
  AssetRepo,
  ListAssetsOpts,
  LiveStream,
  LiveStreamRepo,
  WebhookDelivery,
  WebhookEndpoint,
} from "../engine/index.js";
import type {
  Recording,
  RecordingRepo,
  PlaybackIdRecord,
  PlaybackIdRepo,
  WebhookDeliveryRepo,
  WebhookEndpointRepo,
  WebhookFailure,
  WebhookFailureRepo,
} from "../repo/index.js";
import type { EncodingJobRepo, RenditionRepo } from "../engine/index.js";
import type { EncodingJob, Rendition } from "../engine/types/index.js";

export function createInMemoryAssetRepo(): AssetRepo & { rows: Map<string, Asset> } {
  const rows = new Map<string, Asset>();
  return {
    rows,
    async insert(input) {
      const asset: Asset = { ...input, createdAt: input.createdAt ?? new Date() };
      rows.set(asset.id, asset);
      return asset;
    },
    async byId(id) {
      return rows.get(id) ?? null;
    },
    async byPlaybackId(_playbackId) {
      return null;
    },
    async updateStatus(id, status, fields) {
      const cur = rows.get(id);
      if (!cur) return;
      rows.set(id, { ...cur, ...fields, status });
    },
    async softDelete(id, at) {
      const cur = rows.get(id);
      if (!cur) return;
      rows.set(id, { ...cur, deletedAt: at, status: "deleted" });
    },
    async list(opts: ListAssetsOpts) {
      const all = [...rows.values()]
        .filter((a) => a.projectId === opts.projectId)
        .filter((a) => (opts.includeDeleted ? true : !a.deletedAt))
        .sort((a, b) => b.createdAt.getTime() - a.createdAt.getTime());
      const start = opts.cursor ? all.findIndex((a) => a.createdAt < new Date(opts.cursor!)) : 0;
      const slice = start === -1 ? [] : all.slice(start, start + opts.limit + 1);
      const items = slice.slice(0, opts.limit);
      const result: { items: Asset[]; nextCursor?: string } = { items };
      if (slice.length > opts.limit && items.length > 0) {
        result.nextCursor = items[items.length - 1]!.createdAt.toISOString();
      }
      return result;
    },
    async recent(opts) {
      return [...rows.values()]
        .filter((a) => !a.deletedAt)
        .sort((a, b) => b.createdAt.getTime() - a.createdAt.getTime())
        .slice(0, opts.limit);
    },
  };
}

export function createInMemoryLiveStreamRepo(): LiveStreamRepo & {
  rows: Map<string, LiveStream>;
} {
  const rows = new Map<string, LiveStream>();
  return {
    rows,
    async insert(input) {
      const stream: LiveStream = { ...input, createdAt: input.createdAt ?? new Date() };
      rows.set(stream.id, stream);
      return stream;
    },
    async byId(id) {
      return rows.get(id) ?? null;
    },
    async byStreamKeyHash(hash) {
      for (const s of rows.values()) if (s.streamKeyHash === hash) return s;
      return null;
    },
    async updateStatus(id, status, fields) {
      const cur = rows.get(id);
      if (!cur) return;
      rows.set(id, { ...cur, ...fields, status });
    },
    async active() {
      return [...rows.values()].filter(
        (s) => s.status === "active" || s.status === "reconnecting",
      );
    },
    async sweepStale(cutoff) {
      return [...rows.values()].filter(
        (s) =>
          (s.status === "active" || s.status === "reconnecting") &&
          (!s.lastSeenAt || s.lastSeenAt < cutoff),
      );
    },
  };
}

export function createInMemoryWebhookEndpointRepo(): WebhookEndpointRepo & {
  rows: Map<string, WebhookEndpoint>;
} {
  const rows = new Map<string, WebhookEndpoint>();
  return {
    rows,
    async insert(endpoint) {
      rows.set(endpoint.id, { ...endpoint });
      return endpoint;
    },
    async byId(id) {
      return rows.get(id) ?? null;
    },
    async byProject(projectId) {
      return [...rows.values()]
        .filter((e) => e.projectId === projectId && !e.disabledAt)
        .sort((a, b) => b.createdAt.getTime() - a.createdAt.getTime());
    },
    async updateSecret(id, secret) {
      const cur = rows.get(id);
      if (!cur) return;
      rows.set(id, { ...cur, secret });
    },
    async disable(id, at) {
      const cur = rows.get(id);
      if (!cur) return;
      rows.set(id, { ...cur, disabledAt: at });
    },
  };
}

export function createInMemoryWebhookDeliveryRepo(): WebhookDeliveryRepo & {
  rows: Map<string, WebhookDelivery & { body?: string; signatureHeader?: string }>;
} {
  const rows = new Map<
    string,
    WebhookDelivery & { body?: string; signatureHeader?: string }
  >();
  return {
    rows,
    async insert(delivery) {
      rows.set(delivery.id, { ...delivery });
      return delivery;
    },
    async byId(id) {
      return rows.get(id) ?? null;
    },
    async byEndpoint(endpointId, opts) {
      return [...rows.values()]
        .filter((d) => d.endpointId === endpointId)
        .sort((a, b) => b.createdAt.getTime() - a.createdAt.getTime())
        .slice(0, opts.limit);
    },
    async markDelivered(id, attempts, at) {
      const cur = rows.get(id);
      if (!cur) return;
      rows.set(id, { ...cur, status: "delivered", attempts, deliveredAt: at });
    },
    async markFailed(id, attempts, lastError) {
      const cur = rows.get(id);
      if (!cur) return;
      rows.set(id, { ...cur, status: "failed", attempts, lastError });
    },
  };
}

export function createInMemoryWebhookFailureRepo(): WebhookFailureRepo & {
  rows: Map<string, WebhookFailure>;
} {
  const rows = new Map<string, WebhookFailure>();
  return {
    rows,
    async insert(failure) {
      const f: WebhookFailure = {
        ...failure,
        deadLetteredAt: failure.deadLetteredAt ?? new Date(),
      };
      rows.set(f.id, f);
      return f;
    },
    async byId(id) {
      return rows.get(id) ?? null;
    },
    async list(opts) {
      return [...rows.values()]
        .filter((f) => (opts.endpointId ? f.endpointId === opts.endpointId : true))
        .sort((a, b) => b.deadLetteredAt.getTime() - a.deadLetteredAt.getTime())
        .slice(0, opts.limit);
    },
    async markReplayed(id, at) {
      const cur = rows.get(id);
      if (!cur) return;
      rows.set(id, { ...cur, replayedAt: at });
    },
  };
}

export function createInMemoryRecordingRepo(): RecordingRepo & { rows: Map<string, Recording> } {
  const rows = new Map<string, Recording>();
  return {
    rows,
    async insert(rec) {
      const r: Recording = { ...rec, createdAt: rec.createdAt ?? new Date() };
      rows.set(r.id, r);
      return r;
    },
    async byId(id) {
      return rows.get(id) ?? null;
    },
    async byLiveStream(liveStreamId) {
      return [...rows.values()]
        .filter((r) => r.liveStreamId === liveStreamId)
        .sort((a, b) => b.createdAt.getTime() - a.createdAt.getTime());
    },
    async updateStatus(id, status, fields) {
      const cur = rows.get(id);
      if (!cur) return;
      rows.set(id, { ...cur, ...fields, status });
    },
  };
}

export function createInMemoryEncodingJobRepo(): EncodingJobRepo & {
  rows: Map<string, EncodingJob>;
  deleteByAsset(assetId: string): Promise<void>;
  listByStatuses(statuses: Array<EncodingJob["status"]>, limit: number): Promise<EncodingJob[]>;
} {
  const rows = new Map<string, EncodingJob>();
  return {
    rows,
    async insert(job) {
      const row: EncodingJob = {
        ...job,
        attemptCount: job.attemptCount ?? 0,
        createdAt: job.createdAt ?? new Date(),
      };
      rows.set(row.id, row);
      return row;
    },
    async byId(id) {
      return rows.get(id) ?? null;
    },
    async byAsset(assetId) {
      return [...rows.values()].filter((row) => row.assetId === assetId);
    },
    async queued(assetId, kinds) {
      return [...rows.values()].filter(
        (row) => row.assetId === assetId && row.status === "queued" && kinds.includes(row.kind),
      );
    },
    async updateStatus(id, status, fields) {
      const current = rows.get(id);
      if (!current) return;
      rows.set(id, { ...current, ...fields, status });
    },
    async incrementAttempt(id) {
      const current = rows.get(id);
      if (!current) return;
      rows.set(id, { ...current, attemptCount: current.attemptCount + 1 });
    },
    async deleteByAsset(assetId) {
      for (const [id, row] of rows.entries()) {
        if (row.assetId === assetId) rows.delete(id);
      }
    },
    async listByStatuses(statuses, limit) {
      return [...rows.values()]
        .filter((row) => statuses.includes(row.status))
        .sort((a, b) => b.createdAt.getTime() - a.createdAt.getTime())
        .slice(0, limit);
    },
  };
}

export function createInMemoryRenditionRepo(): RenditionRepo & {
  rows: Map<string, Rendition>;
  deleteByAsset(assetId: string): Promise<void>;
} {
  const rows = new Map<string, Rendition>();
  return {
    rows,
    async insert(rendition) {
      const row: Rendition = {
        ...rendition,
        createdAt: rendition.createdAt ?? new Date(),
      };
      rows.set(row.id, row);
      return row;
    },
    async byAsset(assetId) {
      return [...rows.values()].filter((row) => row.assetId === assetId);
    },
    async updateStatus(id, status, fields) {
      const current = rows.get(id);
      if (!current) return;
      rows.set(id, { ...current, ...fields, status });
    },
    async deleteByAsset(assetId) {
      for (const [id, row] of rows.entries()) {
        if (row.assetId === assetId) rows.delete(id);
      }
    },
  };
}

export function createInMemoryPlaybackIdRepo(): PlaybackIdRepo & {
  rows: Map<string, PlaybackIdRecord>;
} {
  const rows = new Map<string, PlaybackIdRecord>();
  return {
    rows,
    async insert(record) {
      const row: PlaybackIdRecord = {
        ...record,
        createdAt: record.createdAt ?? new Date(),
      };
      rows.set(row.id, row);
      return row;
    },
    async byId(id) {
      return rows.get(id) ?? null;
    },
    async byAsset(assetId) {
      return [...rows.values()].filter((row) => row.assetId === assetId);
    },
    async recent(limit) {
      return [...rows.values()]
        .sort((a, b) => b.createdAt.getTime() - a.createdAt.getTime())
        .slice(0, limit);
    },
    async updatePolicy(id, fields) {
      const current = rows.get(id);
      if (!current) return;
      rows.set(id, {
        ...current,
        policy: fields.policy ?? current.policy,
        tokenRequired: fields.tokenRequired ?? current.tokenRequired,
      });
    },
    async deleteByAsset(assetId) {
      for (const [id, row] of rows.entries()) {
        if (row.assetId === assetId) rows.delete(id);
      }
    },
  };
}
