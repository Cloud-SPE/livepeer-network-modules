// Daydream-portal runtime config. All values come from env so the same
// build can run in dev compose, smoke, and prod without conditional code.
function required(name) {
    const v = process.env[name];
    if (!v || v.trim() === "") {
        throw new Error(`Missing required env var: ${name}`);
    }
    return v;
}
function optional(name, fallback) {
    const v = process.env[name];
    return v && v.trim() !== "" ? v : fallback;
}
function parsePort(raw) {
    const n = Number(raw);
    if (!Number.isInteger(n) || n < 1 || n > 65535) {
        throw new Error(`Invalid port: ${raw}`);
    }
    return n;
}
export function loadConfig() {
    const adminTokensRaw = optional("DAYDREAM_PORTAL_ADMIN_TOKENS", "");
    const adminTokens = adminTokensRaw
        .split(",")
        .map((s) => s.trim())
        .filter((s) => s.length > 0);
    return {
        listen: {
            host: optional("DAYDREAM_PORTAL_LISTEN_HOST", "0.0.0.0"),
            port: parsePort(optional("DAYDREAM_PORTAL_LISTEN_PORT", "8080")),
        },
        postgresUrl: required("DAYDREAM_PORTAL_POSTGRES_URL"),
        authPepper: required("DAYDREAM_PORTAL_AUTH_PEPPER"),
        adminTokens,
        gatewayBaseUrl: required("DAYDREAM_PORTAL_GATEWAY_BASE_URL"),
        uiTokenTtlMs: Number(optional("DAYDREAM_PORTAL_UI_TOKEN_TTL_MS", "3600000")),
        capability: optional("DAYDREAM_PORTAL_CAPABILITY", "daydream-scope"),
    };
}
//# sourceMappingURL=config.js.map