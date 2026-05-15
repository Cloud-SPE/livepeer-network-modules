import type { FastifyInstance } from "fastify";
import type { NodePgDatabase } from "drizzle-orm/node-postgres";
import type { CustomerPortal } from "@livepeer-network-modules/customer-portal";
import type { GatewayClient } from "../service/gatewayClient.js";
export interface RegisterSessionRoutesDeps {
    db: NodePgDatabase<Record<string, unknown>>;
    portal: CustomerPortal;
    gateway: GatewayClient;
    capability: string;
}
export declare function registerSessionRoutes(app: FastifyInstance, deps: RegisterSessionRoutesDeps): void;
