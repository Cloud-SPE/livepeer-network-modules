export interface Config {
    listen: {
        host: string;
        port: number;
    };
    postgresUrl: string;
    authPepper: string;
    adminTokens: string[];
    gatewayBaseUrl: string;
    uiTokenTtlMs: number;
    capability: string;
}
export declare function loadConfig(): Config;
