import { html, nothing, render } from "lit";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";
import { installAdminPageStyles } from "./admin-shared.js";

interface AssetRow {
  id: string;
  projectId: string;
  status: string;
  durationSec: number | null;
  createdAt: string;
  deletedAt: string | null;
}

export class AdminAssets extends HTMLElement {
  private rows: AssetRow[] = [];
  private includeDeleted = false;
  private error: string | null = null;

  private api = new ApiClient({ baseUrl: "" });

  async connectedCallback(): Promise<void> {
    installAdminPageStyles();
    await this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      const q = this.includeDeleted ? "?include_deleted=true" : "";
      const out = await this.api.get<{ items: AssetRow[] }>(`/admin/assets${q}`);
      this.rows = out.items ?? [];
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
    this.draw();
  }

  private async toggleDelete(row: AssetRow): Promise<void> {
    try {
      if (row.deletedAt) {
        await this.api.post(`/admin/assets/${encodeURIComponent(row.id)}/restore`);
      } else {
        await this.api.request("DELETE", `/admin/assets/${encodeURIComponent(row.id)}`);
      }
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "toggle_failed";
      this.draw();
    }
  }

  private draw(): void {
    render(
      html`
      <portal-card heading="Asset Library">
        <portal-detail-section
          heading="VOD pipeline policy"
          description="Current gateway routing split for offline video work."
        >
          <dl class="video-admin-page-meta-list">
            <div class="video-admin-page-meta-item">
              <dt>Baseline</dt>
              <dd><code>video:transcode.abr</code> ABR ladder pipeline, including one-rendition jobs</dd>
            </div>
            <div class="video-admin-page-meta-item">
              <dt>Standard</dt>
              <dd><code>video:transcode.abr</code> ABR ladder pipeline</dd>
            </div>
            <div class="video-admin-page-meta-item">
              <dt>Premium</dt>
              <dd><code>video:transcode.abr</code> ABR ladder pipeline</dd>
            </div>
          </dl>
        </portal-detail-section>
        <portal-data-table
          heading="Asset Inventory"
          description="Inspect active and soft-deleted assets across customer projects."
        >
        <div class="video-admin-page-toolbar" slot="toolbar">
          <label>
            <input
              type="checkbox"
              .checked=${this.includeDeleted}
              @change=${(e: Event): void => {
                this.includeDeleted = (e.target as HTMLInputElement).checked;
                void this.load();
              }}
            />
            Include soft-deleted
          </label>
        </div>
        ${this.error ? html`<p class="video-admin-page-error">${this.error}</p>` : nothing}
        <table class="video-admin-page-table">
          <thead>
            <tr><th>ID</th><th>Project</th><th>Status</th><th>Duration</th><th>Created</th><th>Deleted</th><th></th></tr>
          </thead>
          <tbody>
              ${this.rows.map(
              (r) => html`<tr class=${r.deletedAt ? "video-admin-page-deleted" : ""}>
                <td>${r.id}</td>
                <td>${r.projectId}</td>
                <td>${r.status}</td>
                <td>${r.durationSec !== null ? r.durationSec.toFixed(1) + "s" : "-"}</td>
                <td>${r.createdAt}</td>
                <td>${r.deletedAt ?? ""}</td>
                <td>
                  <portal-button variant=${r.deletedAt ? "ghost" : "danger"} @click=${(): void => void this.toggleDelete(r)}>
                    ${r.deletedAt ? "Restore" : "Soft-delete"}
                  </portal-button>
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

if (!customElements.get("admin-assets")) {
  customElements.define("admin-assets", AdminAssets);
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-assets": AdminAssets;
  }
}
