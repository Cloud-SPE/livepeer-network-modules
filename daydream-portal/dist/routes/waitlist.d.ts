import type { FastifyInstance } from "fastify";
import type { NodePgDatabase } from "drizzle-orm/node-postgres";
export interface RegisterWaitlistRoutesDeps {
    db: NodePgDatabase<Record<string, unknown>>;
}
export declare function registerWaitlistRoutes(app: FastifyInstance, deps: RegisterWaitlistRoutesDeps): void;
