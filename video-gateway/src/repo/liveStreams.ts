import { and, eq, isNull, lt, or } from "drizzle-orm";

import type { Db } from "../db/pool.js";
import { liveStreams } from "../db/schema.js";
import type {
  Capability,
  IngestProtocol,
  LiveStream,
  LiveStreamRepo,
  LiveStreamStatus,
} from "../engine/index.js";

interface LiveStreamRow {
  id: string;
  projectId: string;
  name: string | null;
  streamKeyHash: string;
  status: string;
  ingestProtocol: string;
  recordingEnabled: boolean;
  sessionId: string | null;
  workerId: string | null;
  workerUrl: string | null;
  selectedCapability: string | null;
  selectedOffering: string | null;
  selectedWorkUnit: string | null;
  selectedPricePerWorkUnitWei: string | null;
  paymentWorkId: string | null;
  terminalReason: string | null;
  recordingAssetId: string | null;
  lastSeenAt: Date | null;
  createdAt: Date;
  endedAt: Date | null;
}

function rowToStream(row: LiveStreamRow): LiveStream {
  const s: LiveStream = {
    id: row.id,
    projectId: row.projectId,
    ...(row.name !== null ? { name: row.name } : {}),
    streamKeyHash: row.streamKeyHash,
    status: row.status as LiveStreamStatus,
    ingestProtocol: row.ingestProtocol as IngestProtocol,
    recordingEnabled: row.recordingEnabled,
    createdAt: row.createdAt,
  };
  if (row.sessionId !== null) s.sessionId = row.sessionId;
  if (row.workerId !== null) s.workerId = row.workerId;
  if (row.workerUrl !== null) s.workerUrl = row.workerUrl;
  if (row.selectedCapability !== null) s.selectedCapability = row.selectedCapability as Capability;
  if (row.selectedOffering !== null) s.selectedOffering = row.selectedOffering;
  if (row.selectedWorkUnit !== null) s.selectedWorkUnit = row.selectedWorkUnit;
  if (row.selectedPricePerWorkUnitWei !== null) {
    s.selectedPricePerWorkUnitWei = row.selectedPricePerWorkUnitWei;
  }
  if (row.recordingAssetId !== null) s.recordingAssetId = row.recordingAssetId;
  if (row.lastSeenAt !== null) s.lastSeenAt = row.lastSeenAt;
  if (row.endedAt !== null) s.endedAt = row.endedAt;
  return s;
}

function streamToInsert(
  stream: Omit<LiveStream, "createdAt"> & { createdAt?: Date },
): typeof liveStreams.$inferInsert {
  return {
    id: stream.id,
    projectId: stream.projectId,
    name: stream.name ?? null,
    streamKeyHash: stream.streamKeyHash,
    status: stream.status,
    ingestProtocol: stream.ingestProtocol,
    recordingEnabled: stream.recordingEnabled,
    sessionId: stream.sessionId ?? null,
    workerId: stream.workerId ?? null,
    workerUrl: stream.workerUrl ?? null,
    selectedCapability: stream.selectedCapability ?? null,
    selectedOffering: stream.selectedOffering ?? null,
    selectedWorkUnit: stream.selectedWorkUnit ?? null,
    selectedPricePerWorkUnitWei: stream.selectedPricePerWorkUnitWei ?? null,
    paymentWorkId: null,
    terminalReason: null,
    recordingAssetId: stream.recordingAssetId ?? null,
    lastSeenAt: stream.lastSeenAt ?? null,
    createdAt: stream.createdAt ?? new Date(),
    endedAt: stream.endedAt ?? null,
  };
}

export function createLiveStreamRepo(db: Db): LiveStreamRepo {
  return {
    async insert(stream) {
      const [row] = await db.insert(liveStreams).values(streamToInsert(stream)).returning();
      if (!row) throw new Error("createLiveStreamRepo.insert: no row returned");
      return rowToStream(row as LiveStreamRow);
    },

    async byId(id) {
      const rows = await db.select().from(liveStreams).where(eq(liveStreams.id, id)).limit(1);
      const row = rows[0];
      return row ? rowToStream(row as LiveStreamRow) : null;
    },

    async byStreamKeyHash(hash) {
      const rows = await db
        .select()
        .from(liveStreams)
        .where(eq(liveStreams.streamKeyHash, hash))
        .limit(1);
      const row = rows[0];
      return row ? rowToStream(row as LiveStreamRow) : null;
    },

    async updateStatus(id, status, fields) {
      const update: Partial<typeof liveStreams.$inferInsert> = { status };
      if (fields?.recordingEnabled !== undefined) update.recordingEnabled = fields.recordingEnabled;
      if (fields?.sessionId !== undefined) update.sessionId = fields.sessionId;
      if (fields?.name !== undefined) update.name = fields.name;
      if (fields?.workerId !== undefined) update.workerId = fields.workerId;
      if (fields?.workerUrl !== undefined) update.workerUrl = fields.workerUrl;
      if (fields?.selectedCapability !== undefined) {
        update.selectedCapability = fields.selectedCapability;
      }
      if (fields?.selectedOffering !== undefined) update.selectedOffering = fields.selectedOffering;
      if (fields?.selectedWorkUnit !== undefined) update.selectedWorkUnit = fields.selectedWorkUnit;
      if (fields?.selectedPricePerWorkUnitWei !== undefined) {
        update.selectedPricePerWorkUnitWei = fields.selectedPricePerWorkUnitWei;
      }
      if (fields?.recordingAssetId !== undefined) update.recordingAssetId = fields.recordingAssetId;
      if (fields?.lastSeenAt !== undefined) update.lastSeenAt = fields.lastSeenAt;
      if (fields?.endedAt !== undefined) update.endedAt = fields.endedAt;
      await db.update(liveStreams).set(update).where(eq(liveStreams.id, id));
    },

    async active() {
      const rows = await db
        .select()
        .from(liveStreams)
        .where(or(eq(liveStreams.status, "active"), eq(liveStreams.status, "reconnecting")));
      return rows.map((r) => rowToStream(r as LiveStreamRow));
    },

    async sweepStale(cutoff) {
      const rows = await db
        .select()
        .from(liveStreams)
        .where(
          and(
            or(eq(liveStreams.status, "active"), eq(liveStreams.status, "reconnecting")),
            or(lt(liveStreams.lastSeenAt, cutoff), isNull(liveStreams.lastSeenAt)),
          ),
        );
      return rows.map((r) => rowToStream(r as LiveStreamRow));
    },
  };
}
