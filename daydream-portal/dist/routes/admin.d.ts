import type { FastifyInstance } from "fastify";
import type { NodePgDatabase } from "drizzle-orm/node-postgres";
import type { CustomerPortal, auth } from "@livepeer-network-modules/customer-portal";
export interface RegisterAdminRoutesDeps {
    db: NodePgDatabase<Record<string, unknown>>;
    portal: CustomerPortal;
    adminAuthResolver: auth.AdminAuthResolver;
}
export declare function registerDaydreamAdminRoutes(app: FastifyInstance, deps: RegisterAdminRoutesDeps): void;
