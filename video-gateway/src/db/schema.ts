import {
  bigint,
  boolean,
  check,
  index,
  integer,
  jsonb,
  numeric,
  pgSchema,
  text,
  timestamp,
  unique,
} from "drizzle-orm/pg-core";
import { sql } from "drizzle-orm";

export const media = pgSchema("media");

export const projects = media.table("projects", {
  id: text("id").primaryKey(),
  customerId: text("customer_id").notNull(),
  name: text("name").notNull(),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
});

export const assets = media.table(
  "assets",
  {
    id: text("id").primaryKey(),
    projectId: text("project_id").notNull(),
    status: text("status").notNull(),
    sourceType: text("source_type").notNull(),
    selectedOffering: text("selected_offering"),
    sourceUrl: text("source_url"),
    durationSec: numeric("duration_sec", { precision: 12, scale: 3 }),
    width: integer("width"),
    height: integer("height"),
    frameRate: numeric("frame_rate", { precision: 6, scale: 3 }),
    audioCodec: text("audio_codec"),
    videoCodec: text("video_codec"),
    encodingTier: text("encoding_tier").notNull(),
    ffprobeJson: jsonb("ffprobe_json"),
    errorMessage: text("error_message"),
    createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
    readyAt: timestamp("ready_at", { withTimezone: true }),
    deletedAt: timestamp("deleted_at", { withTimezone: true }),
  },
  (t) => ({
    byProjectCreated: index("assets_project_created").on(t.projectId, t.createdAt),
    byNotDeleted: index("assets_not_deleted").on(t.deletedAt),
  }),
);

export const uploads = media.table("uploads", {
  id: text("id").primaryKey(),
  projectId: text("project_id").notNull(),
  assetId: text("asset_id").references(() => assets.id, { onDelete: "cascade" }),
  status: text("status").notNull(),
  uploadUrl: text("upload_url").notNull(),
  storageKey: text("storage_key").notNull(),
  expiresAt: timestamp("expires_at", { withTimezone: true }).notNull(),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
  completedAt: timestamp("completed_at", { withTimezone: true }),
});

export const renditions = media.table(
  "renditions",
  {
    id: text("id").primaryKey(),
    assetId: text("asset_id")
      .notNull()
      .references(() => assets.id, { onDelete: "cascade" }),
    resolution: text("resolution").notNull(),
    codec: text("codec").notNull(),
    bitrateKbps: integer("bitrate_kbps").notNull(),
    storageKey: text("storage_key"),
    status: text("status").notNull(),
    durationSec: numeric("duration_sec", { precision: 12, scale: 3 }),
    createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
    completedAt: timestamp("completed_at", { withTimezone: true }),
  },
  (t) => ({
    uniqAssetResCodec: unique("renditions_asset_resolution_codec").on(
      t.assetId,
      t.resolution,
      t.codec,
    ),
  }),
);

export const encodingJobs = media.table(
  "encoding_jobs",
  {
    id: text("id").primaryKey(),
    assetId: text("asset_id")
      .notNull()
      .references(() => assets.id, { onDelete: "cascade" }),
    renditionId: text("rendition_id").references(() => renditions.id, {
      onDelete: "cascade",
    }),
    kind: text("kind").notNull(),
    status: text("status").notNull(),
    workerUrl: text("worker_url"),
    attemptCount: integer("attempt_count").notNull().default(0),
    inputUrl: text("input_url"),
    outputPrefix: text("output_prefix"),
    errorMessage: text("error_message"),
    startedAt: timestamp("started_at", { withTimezone: true }),
    completedAt: timestamp("completed_at", { withTimezone: true }),
    createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
  },
  (t) => ({
    byStatusCreated: index("encoding_jobs_status_created").on(t.status, t.createdAt),
  }),
);

export const liveStreams = media.table("live_streams", {
  id: text("id").primaryKey(),
  projectId: text("project_id").notNull(),
  streamKeyHash: text("stream_key_hash").notNull().unique(),
  status: text("status").notNull(),
  ingestProtocol: text("ingest_protocol").notNull(),
  recordingEnabled: boolean("recording_enabled").notNull().default(false),
  sessionId: text("session_id"),
  workerId: text("worker_id"),
  workerUrl: text("worker_url"),
  selectedCapability: text("selected_capability"),
  selectedOffering: text("selected_offering"),
  selectedWorkUnit: text("selected_work_unit"),
  selectedPricePerWorkUnitWei: text("selected_price_per_work_unit_wei"),
  paymentWorkId: text("payment_work_id"),
  terminalReason: text("terminal_reason"),
  recordingAssetId: text("recording_asset_id").references(() => assets.id),
  lastSeenAt: timestamp("last_seen_at", { withTimezone: true }),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
  endedAt: timestamp("ended_at", { withTimezone: true }),
});

export const playbackIds = media.table(
  "playback_ids",
  {
    id: text("id").primaryKey(),
    projectId: text("project_id").notNull(),
    assetId: text("asset_id").references(() => assets.id, { onDelete: "cascade" }),
    liveStreamId: text("live_stream_id").references(() => liveStreams.id, {
      onDelete: "cascade",
    }),
    policy: text("policy").notNull(),
    tokenRequired: boolean("token_required").notNull().default(false),
    createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
  },
  (t) => ({
    exactlyOneTarget: check(
      "playback_ids_one_target",
      sql`(${t.assetId} IS NOT NULL) <> (${t.liveStreamId} IS NOT NULL)`,
    ),
  }),
);

export const liveSessionDebits = media.table("live_session_debits", {
  id: text("id").primaryKey(),
  liveStreamId: text("live_stream_id")
    .notNull()
    .references(() => liveStreams.id, { onDelete: "cascade" }),
  amountUsdMicros: bigint("amount_usd_micros", { mode: "bigint" }).notNull(),
  durationSec: integer("duration_sec").notNull(),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
});

export const webhookEndpoints = media.table("webhook_endpoints", {
  id: text("id").primaryKey(),
  projectId: text("project_id").notNull(),
  url: text("url").notNull(),
  secret: text("secret").notNull(),
  eventTypes: jsonb("event_types"),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
  disabledAt: timestamp("disabled_at", { withTimezone: true }),
});

export const webhookDeliveries = media.table(
  "webhook_deliveries",
  {
    id: text("id").primaryKey(),
    endpointId: text("endpoint_id")
      .notNull()
      .references(() => webhookEndpoints.id, { onDelete: "cascade" }),
    eventType: text("event_type").notNull(),
    body: text("body").notNull(),
    status: text("status").notNull(),
    attempts: integer("attempts").notNull().default(0),
    lastError: text("last_error"),
    createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
    deliveredAt: timestamp("delivered_at", { withTimezone: true }),
  },
  (t) => ({
    byStatusCreated: index("webhook_deliveries_status_created").on(t.status, t.createdAt),
  }),
);

export const webhookFailures = media.table(
  "webhook_failures",
  {
    id: text("id").primaryKey(),
    endpointId: text("endpoint_id")
      .notNull()
      .references(() => webhookEndpoints.id, { onDelete: "cascade" }),
    deliveryId: text("delivery_id").notNull(),
    eventType: text("event_type").notNull(),
    body: text("body").notNull(),
    signatureHeader: text("signature_header").notNull(),
    attemptCount: integer("attempt_count").notNull(),
    lastError: text("last_error").notNull(),
    statusCode: integer("status_code"),
    deadLetteredAt: timestamp("dead_lettered_at", { withTimezone: true })
      .notNull()
      .defaultNow(),
    replayedAt: timestamp("replayed_at", { withTimezone: true }),
  },
  (t) => ({
    byEndpoint: index("webhook_failures_endpoint").on(t.endpointId),
    byDeadLetteredAt: index("webhook_failures_dead_lettered").on(t.deadLetteredAt),
  }),
);

export const recordings = media.table("recordings", {
  id: text("id").primaryKey(),
  liveStreamId: text("live_stream_id")
    .notNull()
    .references(() => liveStreams.id, { onDelete: "cascade" }),
  assetId: text("asset_id").references(() => assets.id, { onDelete: "set null" }),
  status: text("status").notNull(),
  startedAt: timestamp("started_at", { withTimezone: true }),
  endedAt: timestamp("ended_at", { withTimezone: true }),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
});

export const pricingLive = media.table("pricing_live", {
  id: text("id").primaryKey(),
  resolution: text("resolution").notNull(),
  tier: text("tier").notNull(),
  centsPerMinute: numeric("cents_per_minute", { precision: 14, scale: 6 }).notNull(),
  active: boolean("active").notNull().default(true),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
});

export const pricingVod = media.table("pricing_vod", {
  id: text("id").primaryKey(),
  resolution: text("resolution").notNull(),
  codec: text("codec").notNull(),
  tier: text("tier").notNull(),
  centsPerSecond: numeric("cents_per_second", { precision: 14, scale: 6 }).notNull(),
  active: boolean("active").notNull().default(true),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
});

export const usageRecords = media.table("usage_records", {
  id: text("id").primaryKey(),
  projectId: text("project_id").notNull(),
  assetId: text("asset_id").references(() => assets.id, { onDelete: "set null" }),
  liveStreamId: text("live_stream_id").references(() => liveStreams.id, {
    onDelete: "set null",
  }),
  capability: text("capability").notNull(),
  amountCents: numeric("amount_cents", { precision: 14, scale: 6 }).notNull(),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
});

export const schema = {
  projects,
  assets,
  uploads,
  renditions,
  encodingJobs,
  liveStreams,
  playbackIds,
  liveSessionDebits,
  webhookEndpoints,
  webhookDeliveries,
  webhookFailures,
  recordings,
  pricingLive,
  pricingVod,
  usageRecords,
};
