import { desc } from 'drizzle-orm';
import type { Db } from '../db/pool.js';
import { reservations as reservationsTable } from '../db/schema.js';
import * as customersRepo from '../repo/customers.js';
import * as adminAuditEventsRepo from '../repo/adminAuditEvents.js';
import * as reservationsRepo from '../repo/reservations.js';
import * as topupsRepo from '../repo/topups.js';
import { reverseTopup } from '../billing/topups.js';
import type { CustomerRow, CustomerInsert } from '../repo/customers.js';

export interface CreateCustomerInput {
  email: string;
  tier?: 'free' | 'prepaid';
  rateLimitTier?: string;
  initialBalanceUsdCents?: bigint;
  actor: string;
}

export interface AdjustBalanceInput {
  customerId: string;
  deltaUsdCents: bigint;
  reason: string;
  actor: string;
}

export interface RefundTopupInput {
  stripeSessionId: string;
  reason: string;
  actor: string;
}

export interface SetStatusInput {
  customerId: string;
  status: 'active' | 'suspended' | 'closed';
  actor: string;
}

export interface AdminEngine {
  createCustomer(input: CreateCustomerInput): Promise<CustomerRow>;
  getCustomer(id: string): Promise<CustomerRow | null>;
  searchCustomers(input: customersRepo.CustomerSearchInput): Promise<CustomerRow[]>;
  listTopups(input: topupsRepo.TopupSearchInput): Promise<topupsRepo.TopupRow[]>;
  listReservations(input: { customerId?: string; limit: number }): Promise<reservationsRepo.ReservationRow[]>;
  adjustBalance(input: AdjustBalanceInput): Promise<CustomerRow>;
  setStatus(input: SetStatusInput): Promise<boolean>;
  refundTopup(input: RefundTopupInput): Promise<{
    customerId: string;
    amountReversedCents: string;
    newBalanceUsdCents: string;
    alreadyRefunded: boolean;
  }>;
  listAudit(input: adminAuditEventsRepo.AuditSearchInput): Promise<adminAuditEventsRepo.AdminAuditEventRow[]>;
}

export interface AdminEngineDeps {
  db: Db;
}

export function createAdminEngine(deps: AdminEngineDeps): AdminEngine {
  return {
    async createCustomer(input) {
      const insert: CustomerInsert = {
        email: input.email,
        tier: input.tier ?? 'free',
        rateLimitTier: input.rateLimitTier ?? 'default',
        balanceUsdCents: input.initialBalanceUsdCents ?? 0n,
        reservedUsdCents: 0n,
        quotaReservedTokens: 0n,
      };
      const customer = await customersRepo.insertCustomer(deps.db, insert);
      await adminAuditEventsRepo.recordEvent(deps.db, {
        actor: input.actor,
        action: 'customer.create',
        targetId: customer.id,
        payload: JSON.stringify({ email: input.email, tier: insert.tier }),
        statusCode: 201,
      });
      return customer;
    },

    async getCustomer(id) {
      return customersRepo.findById(deps.db, id);
    },

    async searchCustomers(input) {
      return customersRepo.search(deps.db, input);
    },

    async listTopups(input) {
      return topupsRepo.search(deps.db, input);
    },

    async listReservations(input) {
      if (input.customerId) {
        return reservationsRepo.listByCustomer(deps.db, {
          customerId: input.customerId,
          limit: input.limit,
        });
      }
      return deps.db
        .select()
        .from(reservationsTable)
        .orderBy(desc(reservationsTable.createdAt))
        .limit(input.limit);
    },

    async adjustBalance(input) {
      await customersRepo.incrementBalance(deps.db, input.customerId, input.deltaUsdCents);
      const customer = await customersRepo.findById(deps.db, input.customerId);
      if (!customer) throw new Error(`customer ${input.customerId} not found after adjustBalance`);
      await adminAuditEventsRepo.recordEvent(deps.db, {
        actor: input.actor,
        action: 'customer.balance.adjust',
        targetId: input.customerId,
        payload: JSON.stringify({ deltaUsdCents: input.deltaUsdCents.toString(), reason: input.reason }),
        statusCode: 200,
      });
      return customer;
    },

    async setStatus(input) {
      const ok = await customersRepo.setStatus(deps.db, input.customerId, input.status);
      await adminAuditEventsRepo.recordEvent(deps.db, {
        actor: input.actor,
        action: `customer.status.${input.status}`,
        targetId: input.customerId,
        payload: null,
        statusCode: ok ? 200 : 404,
      });
      return ok;
    },

    async refundTopup(input) {
      const result = await reverseTopup(deps.db, {
        stripeSessionId: input.stripeSessionId,
        reason: input.reason,
      });
      await adminAuditEventsRepo.recordEvent(deps.db, {
        actor: input.actor,
        action: 'topup.refund',
        targetId: input.stripeSessionId,
        payload: JSON.stringify({ reason: input.reason, ...result }),
        statusCode: 200,
      });
      return {
        customerId: result.customerId,
        amountReversedCents: result.amountReversedCents,
        newBalanceUsdCents: result.newBalanceUsdCents,
        alreadyRefunded: result.alreadyRefunded,
      };
    },

    async listAudit(input) {
      return adminAuditEventsRepo.search(deps.db, input);
    },
  };
}

export type { CustomerRow };
export { topupsRepo };
