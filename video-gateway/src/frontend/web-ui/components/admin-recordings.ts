import { html, nothing, render } from "lit";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";
import { installAdminPageStyles } from "./admin-shared.js";

interface RecordingRow {
  id: string;
  streamId: string;
  assetId: string | null;
  status: string;
  startedAt: string;
  endedAt: string | null;
  durationSec: number | null;
}

export class AdminRecordings extends HTMLElement {
  private rows: RecordingRow[] = [];
  private error: string | null = null;

  private api = new ApiClient({ baseUrl: "" });

  async connectedCallback(): Promise<void> {
    installAdminPageStyles();
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
    this.draw();
  }

  private draw(): void {
    render(
      html`
      <portal-card heading="Recordings">
        <portal-data-table
          heading="Recorded Sessions"
          description="Review completed and in-progress live recording captures across customer streams."
        >
          ${this.error ? html`<p class="video-admin-page-error">${this.error}</p>` : nothing}
          <table class="video-admin-page-table">
            <thead>
              <tr><th>ID</th><th>Stream</th><th>Asset</th><th>Status</th><th>Duration</th><th>Started</th><th>Ended</th></tr>
            </thead>
            <tbody>
              ${this.rows.map(
                (r) => html`<tr>
                  <td>${r.id}</td>
                  <td>${r.streamId}</td>
                  <td>${r.assetId ?? html`<span class="video-admin-page-dim">-</span>`}</td>
                  <td>
                    <portal-status-pill variant=${r.status === "ready" ? "success" : r.status === "failed" ? "danger" : "info"}>
                      ${r.status}
                    </portal-status-pill>
                  </td>
                  <td>${r.durationSec !== null ? r.durationSec.toFixed(1) + "s" : "-"}</td>
                  <td>${r.startedAt}</td>
                  <td>${r.endedAt ?? html`<span class="video-admin-page-dim">active</span>`}</td>
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

if (!customElements.get("admin-recordings")) {
  customElements.define("admin-recordings", AdminRecordings);
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-recordings": AdminRecordings;
  }
}
