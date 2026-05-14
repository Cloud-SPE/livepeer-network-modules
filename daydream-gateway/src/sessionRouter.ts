/**
 * Per-session orch routing table.
 *
 * Maps `session_id → { orch, brokerUrl, scopeUrl }` so subsequent
 * Scope-API-compatible passthrough calls land on the right orch.
 *
 * Process-scoped; broker / gateway restart drops every in-flight
 * session (consistent with the broker-side store). Persistence is
 * out-of-scope for v0.
 */

import type { ControlWsHandle } from "./controlWsClient.js";
import type { OrchCandidate } from "./orchSelector.js";

export interface SessionRecord {
  sessionId: string;
  orch: OrchCandidate;
  scopeUrl: string;
  controlUrl: string;
  expiresAt: string;
  createdAt: number;
  controlWs?: ControlWsHandle;
}

export class SessionRouter {
  private sessions = new Map<string, SessionRecord>();

  add(rec: SessionRecord): void {
    this.sessions.set(rec.sessionId, rec);
  }

  get(sessionId: string): SessionRecord | undefined {
    return this.sessions.get(sessionId);
  }

  remove(sessionId: string): void {
    this.sessions.delete(sessionId);
  }

  list(): SessionRecord[] {
    return [...this.sessions.values()];
  }
}
