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
    .toolbar { display: flex; gap: 0.5rem; margin-bottom: 0.75rem; }
    input { padding: 0.5rem; border: 1px solid var(--border-1, #d4d4d8); border-radius: 0.375rem; flex: 1; }
    table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
    th, td { padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--border-1, #d4d4d8); text-align: left; vertical-align: top; }
    button { background: none; border: 1px solid var(--border-1, #d4d4d8); border-radius: 0.25rem; padding: 0.25rem 0.5rem; cursor: pointer; font-size: 0.75rem; }
    .replayed { color: #166534; }
    .err { color: #b91c1c; }
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
      <h2>Webhook delivery audit</h2>
      <div class="toolbar">
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
              <td>${r.statusCode ?? "-"}</td>
              <td>${r.lastError}</td>
              <td>${r.deadLetteredAt}</td>
              <td class="replayed">${r.replayedAt ?? ""}</td>
              <td>
                <button
                  ?disabled=${!!this.replayBusy[r.id]}
                  @click=${(): void => void this.replay(r)}
                >
                  ${this.replayBusy[r.id] ? "Replaying." : "Replay"}
                </button>
              </td>
            </tr>`,
          )}
        </tbody>
      </table>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-webhooks": AdminWebhooks;
  }
}
