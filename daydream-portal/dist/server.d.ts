import { type FastifyInstance } from "fastify";
import type { Config } from "./config.js";
export interface BuiltServer {
    app: FastifyInstance;
    close(): Promise<void>;
}
export declare function buildServer(config: Config): Promise<BuiltServer>;
