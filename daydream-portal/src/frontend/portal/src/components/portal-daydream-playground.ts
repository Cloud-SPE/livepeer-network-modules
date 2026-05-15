// Live AI stream surface. The hard work happens inside Scope itself —
// daydream-gateway returns us a scope_url that points at the upstream
// daydreamlive/scope playground UI proxied through the orchestrator.
// We open a session, then iframe the scope_url. WebRTC handshake
// happens inside that iframe; no media touches the portal backend.
//
// On Stop (or page unload) we fire /portal/sessions/:id/close so the
// usage_events row gets its ended_at + duration.

import { DaydreamPortalApi } from "../lib/api.js";

interface ActiveSession {
  sessionId: string;
  scopeUrl: string;
  orchestrator: string | null;
  startedAt: number;
}

export class PortalDaydreamPlayground extends HTMLElement {
  private api = new DaydreamPortalApi();
  private active: ActiveSession | null = null;
  private starting = false;
  private error: string | null = null;
  private unloadHandler: (() => void) | null = null;

  connectedCallback(): void {
    this.render();
  }

  disconnectedCallback(): void {
    void this.stop();
    if (this.unloadHandler) {
      window.removeEventListener("beforeunload", this.unloadHandler);
      this.unloadHandler = null;
    }
  }

  private render(): void {
    if (this.active) {
      this.innerHTML = `
        <portal-card heading="Live session">
          <portal-detail-section>
            <div slot="key">Session ID</div>
            <div slot="value"><code>${this.active.sessionId}</code></div>
          </portal-detail-section>
          ${
            this.active.orchestrator
              ? `<portal-detail-section>
                   <div slot="key">Orchestrator</div>
                   <div slot="value"><code>${this.active.orchestrator}</code></div>
                 </portal-detail-section>`
              : ""
          }
          <iframe
            class="portal-daydream-frame"
            src="${this.active.scopeUrl}"
            allow="camera; microphone; autoplay; clipboard-read; clipboard-write"
            style="width:100%;height:600px;border:1px solid var(--portal-border,#ccc);border-radius:4px"
          ></iframe>
          <portal-action-row>
            <portal-button id="dd-stop" variant="danger">Stop session</portal-button>
          </portal-action-row>
        </portal-card>`;
      this.querySelector("#dd-stop")?.addEventListener("click", () => {
        void this.stop();
      });
      return;
    }

    this.innerHTML = `
      <portal-card heading="Playground">
        <p>Open a live AI streaming session. Your browser will connect
        directly to the orchestrator selected by daydream-gateway; no
        media flows through this portal.</p>
        ${this.error ? `<p class="portal-form-error">${escapeHtml(this.error)}</p>` : ""}
        <portal-action-row>
          <portal-button id="dd-start" ${this.starting ? "disabled" : ""}>
            ${this.starting ? "Opening session…" : "Start session"}
          </portal-button>
        </portal-action-row>
      </portal-card>`;
    this.querySelector("#dd-start")?.addEventListener("click", () => {
      void this.start();
    });
  }

  private async start(): Promise<void> {
    if (this.starting || this.active) return;
    this.starting = true;
    this.error = null;
    this.render();
    try {
      const res = await this.api.openSession();
      this.active = {
        sessionId: res.session_id,
        scopeUrl: res.scope_url,
        orchestrator: res.orchestrator,
        startedAt: Date.now(),
      };
      this.attachUnload();
    } catch (err) {
      this.error = `Could not open session: ${err instanceof Error ? err.message : String(err)}`;
    } finally {
      this.starting = false;
      this.render();
    }
  }

  private async stop(): Promise<void> {
    if (!this.active) return;
    const id = this.active.sessionId;
    this.active = null;
    if (this.unloadHandler) {
      window.removeEventListener("beforeunload", this.unloadHandler);
      this.unloadHandler = null;
    }
    this.render();
    try {
      await this.api.closeSession(id);
    } catch (err) {
      // Best-effort: the backend close handler is idempotent and the
      // gateway will tear the session down on its own once payment
      // stops topping up.
      console.warn("close session failed", err);
    }
  }

  private attachUnload(): void {
    if (this.unloadHandler) return;
    this.unloadHandler = () => {
      if (!this.active) return;
      // Synchronous best-effort tear-down. We can't await here so we
      // rely on keepalive + the gateway's own session timeout.
      try {
        navigator.sendBeacon(
          `/portal/sessions/${encodeURIComponent(this.active.sessionId)}/close`,
        );
      } catch {
        // ignore
      }
    };
    window.addEventListener("beforeunload", this.unloadHandler);
  }
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

customElements.define("portal-daydream-playground", PortalDaydreamPlayground);
