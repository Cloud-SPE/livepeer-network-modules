export interface OpenSessionInput {
    capability: string;
    params?: Record<string, unknown>;
}
export interface OpenSessionResult {
    sessionId: string;
    scopeUrl: string;
    orchestrator?: string;
    raw: unknown;
}
export interface GatewayClient {
    openSession(input: OpenSessionInput): Promise<OpenSessionResult>;
    closeSession(sessionId: string): Promise<void>;
    listOrchestrators(): Promise<unknown>;
}
export interface GatewayClientConfig {
    baseUrl: string;
    fetchTimeoutMs?: number;
}
export declare function createGatewayClient(cfg: GatewayClientConfig): GatewayClient;
