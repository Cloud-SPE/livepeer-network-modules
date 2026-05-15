// Thin client for daydream-gateway. The gateway already handles
// orchestrator resolution, payment minting, and the /v1/cap handshake;
// from this portal's perspective it's just an HTTP service that returns
// a scope_url per session and proxies /api/v1/* to that scope_url.
//
// We never hold the user's media; the browser opens WebRTC directly to
// the orchestrator's TURN once it has the scope_url. The portal backend
// is only responsible for session lifecycle bookkeeping + usage logging.

import { request } from "undici";

export interface OpenSessionInput {
  capability: string;
  // Anything else upstream Scope wants on session open lands in `params`.
  // We pass through opaquely so adding a Scope param doesn't need a
  // portal change.
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

class HttpGatewayClient implements GatewayClient {
  constructor(private readonly cfg: GatewayClientConfig) {}

  async openSession(input: OpenSessionInput): Promise<OpenSessionResult> {
    const url = new URL("/v1/sessions", this.cfg.baseUrl).toString();
    const res = await request(url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        capability: input.capability,
        params: input.params ?? {},
      }),
      bodyTimeout: this.cfg.fetchTimeoutMs ?? 15_000,
      headersTimeout: this.cfg.fetchTimeoutMs ?? 15_000,
    });
    if (res.statusCode >= 400) {
      const text = await res.body.text();
      throw new Error(
        `daydream-gateway open session failed ${res.statusCode}: ${text}`,
      );
    }
    const json = (await res.body.json()) as Record<string, unknown>;
    const sessionId =
      (json.session_id as string | undefined) ??
      (json.sessionId as string | undefined);
    const scopeUrl =
      (json.scope_url as string | undefined) ??
      (json.scopeUrl as string | undefined);
    if (!sessionId || !scopeUrl) {
      throw new Error(
        `daydream-gateway response missing session_id/scope_url: ${JSON.stringify(json)}`,
      );
    }
    return {
      sessionId,
      scopeUrl,
      orchestrator: json.orchestrator as string | undefined,
      raw: json,
    };
  }

  async closeSession(sessionId: string): Promise<void> {
    const url = new URL(
      `/v1/sessions/${encodeURIComponent(sessionId)}`,
      this.cfg.baseUrl,
    ).toString();
    const res = await request(url, {
      method: "DELETE",
      bodyTimeout: this.cfg.fetchTimeoutMs ?? 10_000,
      headersTimeout: this.cfg.fetchTimeoutMs ?? 10_000,
    });
    if (res.statusCode >= 400 && res.statusCode !== 404) {
      const text = await res.body.text();
      throw new Error(
        `daydream-gateway close session failed ${res.statusCode}: ${text}`,
      );
    }
    await res.body.dump();
  }

  async listOrchestrators(): Promise<unknown> {
    const url = new URL("/v1/orchs", this.cfg.baseUrl).toString();
    const res = await request(url, { method: "GET" });
    if (res.statusCode >= 400) {
      throw new Error(`daydream-gateway list orchs failed ${res.statusCode}`);
    }
    return res.body.json();
  }
}

export function createGatewayClient(cfg: GatewayClientConfig): GatewayClient {
  return new HttpGatewayClient(cfg);
}
