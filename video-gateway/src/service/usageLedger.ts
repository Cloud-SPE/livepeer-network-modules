import { randomUUID } from "node:crypto";

import {
  commit as commitReservation,
  refund as refundReservation,
  reserve as reserveReservation,
} from "@livepeer-rewrite/customer-portal/billing";
import {
  customers,
  reservations,
  topups,
  type Db as PortalDb,
} from "@livepeer-rewrite/customer-portal/db";
import { desc, eq, inArray } from "drizzle-orm";

import type { Db as VideoDb } from "../db/pool.js";
import { defaultPricingConfig } from "../engine/config/pricing.js";
import { expandTier } from "../engine/config/encodingLadder.js";
import { reportUsage, estimateCost } from "../engine/service/costQuoter.js";
import type { EncodingTier } from "../engine/types/index.js";
import type { LiveSessionDebitRepo, UsageRecordRepo } from "../repo/index.js";
import { getProjectById } from "./projects.js";

type ReservationRow = typeof reservations.$inferSelect;
type CustomerRow = typeof customers.$inferSelect;

export interface ChargeSummary {
  workId: string;
  reservationId: string | null;
  customerId: string | null;
  kind: "prepaid" | "free" | null;
  state: "open" | "committed" | "refunded" | null;
  estimatedAmountCents: number | null;
  committedAmountCents: number | null;
  refundedAmountCents: number | null;
  capability: string | null;
  model: string | null;
  createdAt: string | null;
  resolvedAt: string | null;
}

export interface CustomerBillingSummary {
  topupTotalCents: number;
  usageCommittedCents: number;
  reservedOpenCents: number;
  refundedCents: number;
}

export interface UsageLedger {
  reserveVodEstimate(input: {
    projectId: string;
    assetId: string;
    encodingTier: EncodingTier;
    estimatedDurationSec?: number | null;
  }): Promise<ChargeSummary | null>;
  recordVodUsage(input: {
    projectId: string;
    assetId: string;
    encodingTier: EncodingTier;
    durationSec: number;
  }): Promise<number>;
  refundVodUsage(input: {
    projectId: string;
    assetId: string;
  }): Promise<ChargeSummary | null>;
  recordLiveUsage(input: {
    projectId: string;
    liveStreamId: string;
    durationSec: number;
  }): Promise<number>;
  getChargeByAsset(assetId: string): Promise<ChargeSummary | null>;
  getChargeByLiveStream(liveStreamId: string): Promise<ChargeSummary | null>;
  listChargesByWorkIds(workIds: string[]): Promise<Map<string, ChargeSummary>>;
  summarizeCustomer(customerId: string): Promise<CustomerBillingSummary>;
}

export interface UsageLedgerDeps {
  portalDb: PortalDb;
  videoDb: VideoDb;
  usageRecords: UsageRecordRepo;
  liveSessionDebits: LiveSessionDebitRepo;
}

export function vodWorkId(assetId: string): string {
  return `video:asset:${assetId}`;
}

export function liveWorkId(liveStreamId: string): string {
  return `video:live:${liveStreamId}`;
}

export function usageWorkId(row: {
  assetId: string | null;
  liveStreamId: string | null;
}): string | null {
  if (row.assetId) return vodWorkId(row.assetId);
  if (row.liveStreamId) return liveWorkId(row.liveStreamId);
  return null;
}

export function createUsageLedger(deps: UsageLedgerDeps): UsageLedger {
  return {
    async reserveVodEstimate(input) {
      const customer = await customerForProject(deps, input.projectId);
      if (!customer || customer.tier !== "prepaid") {
        return getChargeByWorkId(deps.portalDb, vodWorkId(input.assetId));
      }
      const estimatedDurationSec =
        input.estimatedDurationSec && Number.isFinite(input.estimatedDurationSec)
          ? input.estimatedDurationSec
          : null;
      if (estimatedDurationSec === null) {
        return getChargeByWorkId(deps.portalDb, vodWorkId(input.assetId));
      }
      const estimate = estimateCost({
        capability: "video:transcode.abr",
        callerTier: customer.tier,
        renditions: expandTier(input.encodingTier),
        estimatedSeconds: estimatedDurationSec,
        pricing: defaultPricingConfig(),
      });
      await ensureOpenReservation(deps.portalDb, customer.id, vodWorkId(input.assetId), estimate.cents);
      return getChargeByWorkId(deps.portalDb, vodWorkId(input.assetId));
    },

    async recordVodUsage(input) {
      const existing = await deps.usageRecords.byAsset(input.assetId);
      if (existing.length > 0) {
        return existing[0]!.amountCents;
      }
      const usage = reportUsage({
        capability: "video:transcode.abr",
        renditions: expandTier(input.encodingTier),
        actualSeconds: input.durationSec,
        pricing: defaultPricingConfig(),
      });
      const customer = await customerForProject(deps, input.projectId);
      if (customer?.tier === "prepaid") {
        await commitExactCharge(
          deps.portalDb,
          customer.id,
          vodWorkId(input.assetId),
          usage.cents,
          "video:transcode.abr",
          `abr:${input.encodingTier}`,
        );
      }
      await deps.usageRecords.insert({
        id: `usage_${randomUUID().replaceAll("-", "").slice(0, 16)}`,
        projectId: input.projectId,
        assetId: input.assetId,
        liveStreamId: null,
        capability: "video:transcode.abr",
        amountCents: usage.cents,
      });
      return usage.cents;
    },

    async refundVodUsage(input) {
      const customer = await customerForProject(deps, input.projectId);
      if (!customer || customer.tier !== "prepaid") {
        return getChargeByWorkId(deps.portalDb, vodWorkId(input.assetId));
      }
      await refundOpenReservation(deps.portalDb, vodWorkId(input.assetId));
      return getChargeByWorkId(deps.portalDb, vodWorkId(input.assetId));
    },

    async recordLiveUsage(input) {
      const existingDebit = await deps.liveSessionDebits.byLiveStream(input.liveStreamId);
      if (existingDebit.length > 0) {
        const existingUsage = await deps.usageRecords.byLiveStream(input.liveStreamId);
        return existingUsage[0]?.amountCents ?? Math.round(Number(existingDebit[0]!.amountUsdMicros) / 10_000);
      }
      const pricing = defaultPricingConfig();
      const rawCents = Math.ceil(pricing.liveCentsPerSecond * input.durationSec);
      const customer = await customerForProject(deps, input.projectId);
      if (customer?.tier === "prepaid") {
        await commitExactCharge(
          deps.portalDb,
          customer.id,
          liveWorkId(input.liveStreamId),
          rawCents,
          "video:live.rtmp",
          "rtmp-live",
        );
      }
      await deps.liveSessionDebits.insert({
        id: `lsd_${randomUUID().replaceAll("-", "").slice(0, 16)}`,
        liveStreamId: input.liveStreamId,
        amountUsdMicros: BigInt(Math.round(rawCents * 10_000)),
        durationSec: input.durationSec,
      });
      await deps.usageRecords.insert({
        id: `usage_${randomUUID().replaceAll("-", "").slice(0, 16)}`,
        projectId: input.projectId,
        assetId: null,
        liveStreamId: input.liveStreamId,
        capability: "video:live.rtmp",
        amountCents: rawCents,
      });
      return rawCents;
    },

    async getChargeByAsset(assetId) {
      return getChargeByWorkId(deps.portalDb, vodWorkId(assetId));
    },

    async getChargeByLiveStream(liveStreamId) {
      return getChargeByWorkId(deps.portalDb, liveWorkId(liveStreamId));
    },

    async listChargesByWorkIds(workIds) {
      return listChargesByWorkIds(deps.portalDb, workIds);
    },

    async summarizeCustomer(customerId) {
      return summarizeCustomerCharges(deps.portalDb, customerId);
    },
  };
}

async function customerForProject(
  deps: UsageLedgerDeps,
  projectId: string,
): Promise<CustomerRow | null> {
  const project = await getProjectById(deps.videoDb, projectId);
  if (!project) return null;
  const rows = await deps.portalDb
    .select()
    .from(customers)
    .where(eq(customers.id, project.customerId))
    .limit(1);
  return rows[0] ?? null;
}

async function ensureOpenReservation(
  portalDb: PortalDb,
  customerId: string,
  workId: string,
  estimateCents: number,
): Promise<ReservationRow> {
  const existing = await findReservationByWorkId(portalDb, workId);
  if (existing) return existing;
  await reserveReservation(portalDb, {
    customerId,
    workId,
    estCostCents: BigInt(Math.max(0, Math.ceil(estimateCents))),
  });
  const inserted = await findReservationByWorkId(portalDb, workId);
  if (!inserted) {
    throw new Error(`ensureOpenReservation: reservation ${workId} missing after reserve`);
  }
  return inserted;
}

async function commitExactCharge(
  portalDb: PortalDb,
  customerId: string,
  workId: string,
  actualCents: number,
  capability: string,
  model: string,
): Promise<ReservationRow> {
  const exact = Math.max(0, Math.ceil(actualCents));
  let reservation = await findReservationByWorkId(portalDb, workId);
  if (reservation?.state === "committed" || reservation?.state === "refunded") {
    return reservation;
  }
  if (!reservation) {
    reservation = await ensureOpenReservation(portalDb, customerId, workId, exact);
  }
  const reserved = reservation.amountUsdCents ? Number(reservation.amountUsdCents) : 0;
  if (reserved < exact) {
    await refundReservation(portalDb, reservation.id);
    reservation = await ensureOpenReservation(portalDb, customerId, workId, exact);
  }
  await commitReservation(portalDb, {
    reservationId: reservation.id,
    actualCostCents: BigInt(exact),
    capability,
    model,
    tier: "prepaid",
  });
  const committed = await findReservationByWorkId(portalDb, workId);
  if (!committed) {
    throw new Error(`commitExactCharge: reservation ${workId} missing after commit`);
  }
  return committed;
}

async function refundOpenReservation(portalDb: PortalDb, workId: string): Promise<void> {
  const reservation = await findReservationByWorkId(portalDb, workId);
  if (!reservation || reservation.state !== "open") return;
  await refundReservation(portalDb, reservation.id);
}

async function findReservationByWorkId(
  portalDb: PortalDb,
  workId: string,
): Promise<ReservationRow | null> {
  const rows = await portalDb
    .select()
    .from(reservations)
    .where(eq(reservations.workId, workId))
    .limit(1);
  return rows[0] ?? null;
}

async function getChargeByWorkId(
  portalDb: PortalDb,
  workId: string,
): Promise<ChargeSummary | null> {
  const row = await findReservationByWorkId(portalDb, workId);
  return row ? rowToChargeSummary(row) : null;
}

async function listChargesByWorkIds(
  portalDb: PortalDb,
  workIds: string[],
): Promise<Map<string, ChargeSummary>> {
  if (workIds.length === 0) return new Map();
  const rows = await portalDb
    .select()
    .from(reservations)
    .where(inArray(reservations.workId, [...new Set(workIds)]));
  return new Map(rows.map((row) => [row.workId, rowToChargeSummary(row)]));
}

async function summarizeCustomerCharges(
  portalDb: PortalDb,
  customerId: string,
): Promise<CustomerBillingSummary> {
  const reservationRows = await portalDb
    .select()
    .from(reservations)
    .where(eq(reservations.customerId, customerId))
    .orderBy(desc(reservations.createdAt));
  const videoReservations = reservationRows.filter((row) => isVideoWorkId(row.workId));
  const topupRows = await portalDb
    .select()
    .from(topups)
    .where(eq(topups.customerId, customerId))
    .orderBy(desc(topups.createdAt));
  return {
    topupTotalCents: topupRows
      .filter((row) => row.status === "succeeded")
      .reduce((sum, row) => sum + Number(row.amountUsdCents), 0),
    usageCommittedCents: videoReservations.reduce(
      (sum, row) => sum + Number(row.committedUsdCents ?? 0n),
      0,
    ),
    reservedOpenCents: videoReservations
      .filter((row) => row.state === "open")
      .reduce((sum, row) => sum + Number(row.amountUsdCents ?? 0n), 0),
    refundedCents: videoReservations.reduce(
      (sum, row) => sum + Number(row.refundedUsdCents ?? 0n),
      0,
    ),
  };
}

function rowToChargeSummary(row: ReservationRow): ChargeSummary {
  return {
    workId: row.workId,
    reservationId: row.id,
    customerId: row.customerId,
    kind: row.kind,
    state: row.state,
    estimatedAmountCents: maybeBigintToNumber(row.amountUsdCents),
    committedAmountCents: maybeBigintToNumber(row.committedUsdCents),
    refundedAmountCents: maybeBigintToNumber(row.refundedUsdCents),
    capability: row.capability,
    model: row.model,
    createdAt: row.createdAt.toISOString(),
    resolvedAt: row.resolvedAt?.toISOString() ?? null,
  };
}

function maybeBigintToNumber(value: bigint | null): number | null {
  return value === null ? null : Number(value);
}

function isVideoWorkId(workId: string): boolean {
  return workId.startsWith("video:asset:") || workId.startsWith("video:live:");
}
