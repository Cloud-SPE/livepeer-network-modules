import { html, nothing, render } from "lit";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";
import { installAdminPageStyles } from "./admin-shared.js";

interface FailureRow {
  id: string;
  endpointId: string;
  projectId: string | null;
  deliveryId: string;
  eventType: string;
  attemptCount: number;
  statusCode: number | null;
  lastError: string;
  deadLetteredAt: string;
  replayedAt: string | null;
}

export class AdminWebhooks extends HTMLElement {
  private rows: FailureRow[] = [];
  private endpointFilter = "";
  private projectFilter = "";
  private error: string | null = null;
  private replayBusy: Record<string, boolean> = {};

  private api = new ApiClient({ baseUrl: "" });

  async connectedCallback(): Promise<void> {
    installAdminPageStyles();
    await this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      const params = new URLSearchParams();
      if (this.endpointFilter.trim()) params.set("endpoint_id", this.endpointFilter.trim());
      if (this.projectFilter.trim()) params.set("project_id", this.projectFilter.trim());
      const q = params.size > 0 ? `?${params.toString()}` : "";
      const out = await this.api.get<{ items: FailureRow[] }>(`/admin/webhook-failures${q}`);
      this.rows = out.items ?? [];
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
    this.draw();
  }

  private async replay(row: FailureRow): Promise<void> {
    this.replayBusy = { ...this.replayBusy, [row.id]: true };
    try {
      await this.api.post(`/admin/webhook-failures/${encodeURIComponent(row.id)}/replay`);
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "replay_failed";
    } finally {
      const next = { ...this.replayBusy };
      delete next[row.id];
      this.replayBusy = next;
      this.draw();
    }
  }

  private draw(): void {
    render(
      html`
      <portal-card heading="Webhook Delivery Audit">
        <portal-data-table
          heading="Delivery Failures"
          description="Inspect dead-lettered webhook deliveries, replay them, and filter by endpoint."
        >
          <div class="video-admin-page-toolbar video-admin-page-toolbar--grow" slot="toolbar">
            <input
              class="video-admin-page-toolbar-input"
              placeholder="filter by endpoint id"
              .value=${this.endpointFilter}
              @input=${(e: Event): void => {
                this.endpointFilter = (e.target as HTMLInputElement).value;
              }}
              @keydown=${(e: KeyboardEvent): void => {
                if (e.key === "Enter") void this.load();
              }}
            />
            <input
              class="video-admin-page-toolbar-input"
              placeholder="filter by project id"
              .value=${this.projectFilter}
              @input=${(e: Event): void => {
                this.projectFilter = (e.target as HTMLInputElement).value;
              }}
              @keydown=${(e: KeyboardEvent): void => {
                if (e.key === "Enter") void this.load();
              }}
            />
            <portal-button @click=${(): void => void this.load()}>Refresh</portal-button>
          </div>
          ${this.error ? html`<p class="video-admin-page-error">${this.error}</p>` : nothing}
          <table class="video-admin-page-table">
            <thead>
              <tr>
                <th>ID</th><th>Project</th><th>Endpoint</th><th>Event</th><th>Attempts</th><th>Status</th>
                <th>Last error</th><th>Dead-lettered</th><th>Replayed</th><th></th>
              </tr>
            </thead>
            <tbody>
              ${this.rows.map(
                (r) => html`<tr>
                  <td>${r.id}</td>
                  <td>${r.projectId ?? "—"}</td>
                  <td>${r.endpointId}</td>
                  <td>${r.eventType}</td>
                  <td>${r.attemptCount}</td>
                  <td>
                    <portal-status-pill variant=${r.statusCode && r.statusCode >= 500 ? "danger" : r.statusCode && r.statusCode >= 400 ? "warning" : "neutral"}>
                      ${r.statusCode ?? "unknown"}
                    </portal-status-pill>
                  </td>
                  <td>${r.lastError || html`<span class="video-admin-page-dim">-</span>`}</td>
                  <td>${r.deadLetteredAt}</td>
                  <td>
                    ${r.replayedAt
                      ? html`<portal-status-pill variant="success">${r.replayedAt}</portal-status-pill>`
                      : html`<span class="video-admin-page-dim">pending</span>`}
                  </td>
                  <td>
                    <portal-action-row align="end">
                      <portal-button
                        variant="ghost"
                        ?disabled=${!!this.replayBusy[r.id]}
                        @click=${(): void => void this.replay(r)}
                      >
                        ${this.replayBusy[r.id] ? "Replaying." : "Replay"}
                      </portal-button>
                    </portal-action-row>
                  </td>
                </tr>`,
              )}
            </tbody>
          </table>
        </portal-data-table>
      </portal-card>
      `,
      this,
    );
  }
}

if (!customElements.get("admin-webhooks")) {
  customElements.define("admin-webhooks", AdminWebhooks);
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-webhooks": AdminWebhooks;
  }
}
