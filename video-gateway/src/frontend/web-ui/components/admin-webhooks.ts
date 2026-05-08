import { LitElement, css, html, type TemplateResult } from "lit";
import { customElement, state } from "lit/decorators.js";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";

interface FailureRow {
  id: string;
  endpointId: string;
  deliveryId: string;
  eventType: string;
  attemptCount: number;
  statusCode: number | null;
  lastError: string;
  deadLetteredAt: string;
  replayedAt: string | null;
}

@customElement("admin-webhooks")
export class AdminWebhooks extends LitElement {
  @state() private rows: FailureRow[] = [];
  @state() private endpointFilter = "";
  @state() private error: string | null = null;
  @state() private replayBusy: Record<string, boolean> = {};

  private api = new ApiClient({ baseUrl: "" });

  static styles = css`
    :host { display: block; }
    .toolbar { display: flex; gap: var(--space-2); flex: 1; }
    input { flex: 1; min-height: 2.75rem; }
    table { width: 100%; border-collapse: collapse; font-size: var(--font-size-sm); }
    th, td { padding: 0.75rem 0.8rem; border-bottom: 1px solid var(--border-1); text-align: left; vertical-align: top; }
    th {
      color: var(--text-3);
      font-size: var(--font-size-xs);
      font-weight: 650;
      text-transform: uppercase;
      letter-spacing: 0.1em;
    }
    tbody tr:hover { background: rgba(255,255,255,0.02); }
    .replayed { color: var(--success); }
    .err { color: var(--danger); }
    .dim { color: var(--text-3); }
  `;

  override async connectedCallback(): Promise<void> {
    super.connectedCallback();
    await this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      const q = this.endpointFilter.trim()
        ? `?endpoint_id=${encodeURIComponent(this.endpointFilter.trim())}`
        : "";
      const out = await this.api.get<{ items: FailureRow[] }>(`/admin/webhook-failures${q}`);
      this.rows = out.items ?? [];
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
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
    }
  }

  render(): TemplateResult {
    return html`
      <portal-card heading="Webhook Delivery Audit">
        <portal-data-table
          heading="Delivery Failures"
          description="Inspect dead-lettered webhook deliveries, replay them, and filter by endpoint."
        >
          <div class="toolbar" slot="toolbar">
            <input
              placeholder="filter by endpoint id"
              .value=${this.endpointFilter}
              @input=${(e: Event): void => {
                this.endpointFilter = (e.target as HTMLInputElement).value;
              }}
              @keydown=${(e: KeyboardEvent): void => {
                if (e.key === "Enter") void this.load();
              }}
            />
            <portal-button @click=${(): void => void this.load()}>Refresh</portal-button>
          </div>
          ${this.error ? html`<p class="err">${this.error}</p>` : ""}
          <table>
            <thead>
              <tr>
                <th>ID</th><th>Endpoint</th><th>Event</th><th>Attempts</th><th>Status</th>
                <th>Last error</th><th>Dead-lettered</th><th>Replayed</th><th></th>
              </tr>
            </thead>
            <tbody>
              ${this.rows.map(
                (r) => html`<tr>
                  <td>${r.id}</td>
                  <td>${r.endpointId}</td>
                  <td>${r.eventType}</td>
                  <td>${r.attemptCount}</td>
                  <td>
                    <portal-status-pill variant=${r.statusCode && r.statusCode >= 500 ? "danger" : r.statusCode && r.statusCode >= 400 ? "warning" : "neutral"}>
                      ${r.statusCode ?? "unknown"}
                    </portal-status-pill>
                  </td>
                  <td>${r.lastError || html`<span class="dim">-</span>`}</td>
                  <td>${r.deadLetteredAt}</td>
                  <td>
                    ${r.replayedAt
                      ? html`<portal-status-pill variant="success">${r.replayedAt}</portal-status-pill>`
                      : html`<span class="dim">pending</span>`}
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
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-webhooks": AdminWebhooks;
  }
}
