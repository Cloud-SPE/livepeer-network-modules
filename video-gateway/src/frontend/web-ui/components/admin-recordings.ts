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
    table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
    th, td { padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--border-1, #d4d4d8); text-align: left; }
    .err { color: #b91c1c; }
    a { color: var(--accent-1, #2563eb); }
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
      <h2>Recordings</h2>
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
              <td>${r.assetId ?? "-"}</td>
              <td>${r.status}</td>
              <td>${r.durationSec !== null ? r.durationSec.toFixed(1) + "s" : "-"}</td>
              <td>${r.startedAt}</td>
              <td>${r.endedAt ?? ""}</td>
            </tr>`,
          )}
        </tbody>
      </table>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-recordings": AdminRecordings;
  }
}
