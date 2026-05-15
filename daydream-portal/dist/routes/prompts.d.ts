import type { FastifyInstance } from "fastify";
import type { NodePgDatabase } from "drizzle-orm/node-postgres";
import type { CustomerPortal } from "@livepeer-network-modules/customer-portal";
export interface RegisterPromptRoutesDeps {
    db: NodePgDatabase<Record<string, unknown>>;
    portal: CustomerPortal;
}
export declare function registerPromptRoutes(app: FastifyInstance, deps: RegisterPromptRoutesDeps): void;
