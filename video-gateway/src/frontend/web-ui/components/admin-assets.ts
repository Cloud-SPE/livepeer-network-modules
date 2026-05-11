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
  playbackId?: string | null;
  playbackUrl?: string | null;
  renditions?: Array<{
    id: string;
    resolution: string;
    codec: string;
    status: string;
  }>;
  jobs?: Array<{
    id: string;
    kind: string;
    status: string;
    errorMessage: string | null;
  }>;
}

export class AdminAssets extends HTMLElement {
  private rows: AssetRow[] = [];
  private includeDeleted = false;
  private projectFilter = "";
  private error: string | null = null;

  private api = new ApiClient({ baseUrl: "" });

  async connectedCallback(): Promise<void> {
    installAdminPageStyles();
    await this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      const params = new URLSearchParams();
      if (this.includeDeleted) params.set("include_deleted", "true");
      if (this.projectFilter.trim()) params.set("project_id", this.projectFilter.trim());
      const q = params.size > 0 ? `?${params.toString()}` : "";
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

  private async retry(row: AssetRow): Promise<void> {
    try {
      await this.api.post(`/admin/assets/${encodeURIComponent(row.id)}/retry`);
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "retry_failed";
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
            <tr><th>ID</th><th>Project</th><th>Status</th><th>Duration</th><th>Created</th><th>Playback</th><th>Execution</th><th></th></tr>
          </thead>
          <tbody>
              ${this.rows.map(
              (r) => html`<tr class=${r.deletedAt ? "video-admin-page-deleted" : ""}>
                <td>${r.id}</td>
                <td>${r.projectId}</td>
                <td>${r.status}</td>
                <td>${r.durationSec !== null ? r.durationSec.toFixed(1) + "s" : "-"}</td>
                <td>${r.createdAt}</td>
                <td>
                  ${r.playbackUrl
                    ? html`<a class="video-admin-page-link" href=${r.playbackUrl}>${r.playbackId ?? "playback"}</a>`
                    : html`<span class="video-admin-page-dim">pending</span>`}
                </td>
                <td>
                  <details>
                    <summary>${(r.jobs?.length ?? 0)} jobs · ${(r.renditions?.length ?? 0)} renditions</summary>
                    <div class="video-admin-page-stack">
                      ${(r.jobs ?? []).map(
                        (job) => html`<div>
                          <strong>${job.kind}</strong>: ${job.status}
                          ${job.errorMessage ? html`<div class="video-admin-page-error">${job.errorMessage}</div>` : nothing}
                        </div>`,
                      )}
                      ${(r.renditions ?? []).map(
                        (rendition) => html`<div>${rendition.resolution} ${rendition.codec} · ${rendition.status}</div>`,
                      )}
                      ${r.deletedAt ? html`<div class="video-admin-page-dim">deleted at ${r.deletedAt}</div>` : nothing}
                    </div>
                  </details>
                </td>
                <td>
                  <portal-action-row align="end">
                    ${!r.deletedAt && r.status !== "ready"
                      ? html`
                          <portal-button variant="ghost" @click=${(): void => void this.retry(r)}>
                            Retry
                          </portal-button>
                        `
                      : nothing}
                    <portal-button variant=${r.deletedAt ? "ghost" : "danger"} @click=${(): void => void this.toggleDelete(r)}>
                      ${r.deletedAt ? "Restore" : "Soft-delete"}
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

if (!customElements.get("admin-assets")) {
  customElements.define("admin-assets", AdminAssets);
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-assets": AdminAssets;
  }
}
