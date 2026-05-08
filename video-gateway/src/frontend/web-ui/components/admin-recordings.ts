import { LitElement, css, html, type TemplateResult } from "lit";
import { customElement, state } from "lit/decorators.js";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";

interface RecordingRow {
  id: string;
  streamId: string;
  assetId: string | null;
  status: string;
  startedAt: string;
  endedAt: string | null;
  durationSec: number | null;
}

@customElement("admin-recordings")
export class AdminRecordings extends LitElement {
  @state() private rows: RecordingRow[] = [];
  @state() private error: string | null = null;

  private api = new ApiClient({ baseUrl: "" });

  static styles = css`
    :host { display: block; }
    table { width: 100%; border-collapse: collapse; font-size: var(--font-size-sm); }
    th, td { padding: 0.75rem 0.8rem; border-bottom: 1px solid var(--border-1); text-align: left; }
    th {
      color: var(--text-3);
      font-size: var(--font-size-xs);
      font-weight: 650;
      text-transform: uppercase;
      letter-spacing: 0.1em;
    }
    tbody tr:hover { background: rgba(255,255,255,0.02); }
    .err { color: var(--danger); }
    a { color: var(--accent); }
    .dim { color: var(--text-3); }
  `;

  override async connectedCallback(): Promise<void> {
    super.connectedCallback();
    await this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      const out = await this.api.get<{ items: RecordingRow[] }>(`/admin/recordings`);
      this.rows = out.items ?? [];
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
  }

  render(): TemplateResult {
    return html`
      <portal-card heading="Recordings">
        <portal-data-table
          heading="Recorded Sessions"
          description="Review completed and in-progress live recording captures across customer streams."
        >
          ${this.error ? html`<p class="err">${this.error}</p>` : ""}
          <table>
            <thead>
              <tr><th>ID</th><th>Stream</th><th>Asset</th><th>Status</th><th>Duration</th><th>Started</th><th>Ended</th></tr>
            </thead>
            <tbody>
              ${this.rows.map(
                (r) => html`<tr>
                  <td>${r.id}</td>
                  <td>${r.streamId}</td>
                  <td>${r.assetId ?? html`<span class="dim">-</span>`}</td>
                  <td>
                    <portal-status-pill variant=${r.status === "ready" ? "success" : r.status === "failed" ? "danger" : "info"}>
                      ${r.status}
                    </portal-status-pill>
                  </td>
                  <td>${r.durationSec !== null ? r.durationSec.toFixed(1) + "s" : "-"}</td>
                  <td>${r.startedAt}</td>
                  <td>${r.endedAt ?? html`<span class="dim">active</span>`}</td>
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
    "admin-recordings": AdminRecordings;
  }
}
