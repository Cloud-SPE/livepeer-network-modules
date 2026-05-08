import { LitElement, css, html, type TemplateResult } from "lit";
import { customElement, state } from "lit/decorators.js";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";

interface AssetRow {
  id: string;
  status: string;
  durationSec: number | null;
  createdAt: string;
  deletedAt: string | null;
  playbackUrl: string | null;
}

@customElement("portal-assets")
export class PortalAssets extends LitElement {
  @state() private rows: AssetRow[] = [];
  @state() private uploading = false;
  @state() private error: string | null = null;
  @state() private confirmDelete: AssetRow | null = null;

  private api = new ApiClient({ baseUrl: "" });

  static styles = css`
    :host { display: block; }
    .toolbar { display: flex; gap: var(--space-2); align-items: center; }
    table { width: 100%; border-collapse: collapse; font-size: var(--font-size-sm); }
    th, td { padding: 0.7rem 0.75rem; border-bottom: 1px solid var(--border-1); text-align: left; }
    th {
      color: var(--text-3);
      font-size: var(--font-size-xs);
      font-weight: 650;
      text-transform: uppercase;
      letter-spacing: 0.1em;
    }
    tbody tr:hover { background: rgba(255, 255, 255, 0.02); }
    .deleted { color: var(--text-3); }
    button { background: rgba(255, 255, 255, 0.03); border: 1px solid var(--border-1); border-radius: var(--radius-pill); padding: 0.35rem 0.65rem; cursor: pointer; font-size: 0.75rem; color: var(--text-1); }
    .err { color: var(--danger); }
    a { color: var(--accent); }
  `;

  override async connectedCallback(): Promise<void> {
    super.connectedCallback();
    await this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      const out = await this.api.get<{ items: AssetRow[] }>(`/portal/assets`);
      this.rows = out.items ?? [];
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
  }

  private async upload(file: File): Promise<void> {
    this.uploading = true;
    this.error = null;
    try {
      const init = await this.api.post<{ uploadUrl: string; assetId: string }>(`/portal/uploads`, {
        filename: file.name,
        size: file.size,
        contentType: file.type,
      });
      const tus = await fetch(init.uploadUrl, {
        method: "PATCH",
        headers: {
          "tus-resumable": "1.0.0",
          "upload-offset": "0",
          "content-type": "application/offset+octet-stream",
        },
        body: file,
      });
      if (!tus.ok) throw new Error(`upload_failed_${tus.status}`);
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "upload_failed";
    } finally {
      this.uploading = false;
    }
  }

  private onFile(e: Event): void {
    const input = e.target as HTMLInputElement;
    const file = input.files?.[0];
    if (file) void this.upload(file);
    input.value = "";
  }

  private async softDelete(row: AssetRow): Promise<void> {
    try {
      await this.api.request("DELETE", `/portal/assets/${encodeURIComponent(row.id)}`);
      this.confirmDelete = null;
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "delete_failed";
    }
  }

  private async restore(row: AssetRow): Promise<void> {
    try {
      await this.api.post(`/portal/assets/${encodeURIComponent(row.id)}/restore`);
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "restore_failed";
    }
  }

  render(): TemplateResult {
    return html`
      <portal-card heading="Asset library">
        <portal-data-table
          heading="Library"
          description="Upload, review, restore, and retire video assets from one place."
        >
        <div class="toolbar" slot="toolbar">
          <input type="file" @change=${this.onFile} ?disabled=${this.uploading} />
          ${this.uploading ? html`<span>Uploading.</span>` : ""}
        </div>
        ${this.error ? html`<p class="err">${this.error}</p>` : ""}
        <table>
          <thead>
            <tr><th>ID</th><th>Status</th><th>Duration</th><th>Created</th><th>Playback</th><th></th></tr>
          </thead>
          <tbody>
            ${this.rows.map(
              (r) => html`<tr class=${r.deletedAt ? "deleted" : ""}>
                <td>${r.id}</td>
                <td>${r.status}${r.deletedAt ? " (deleted)" : ""}</td>
                <td>${r.durationSec !== null ? r.durationSec.toFixed(1) + "s" : "-"}</td>
                <td>${r.createdAt}</td>
                <td>${r.playbackUrl ? html`<a href=${r.playbackUrl}>play</a>` : "-"}</td>
                <td>
                  ${r.deletedAt
                    ? html`<button @click=${(): void => void this.restore(r)}>Restore</button>`
                    : html`<button
                        @click=${(): void => {
                          this.confirmDelete = r;
                        }}
                      >
                        Delete
                      </button>`}
                </td>
              </tr>`,
            )}
          </tbody>
        </table>
        </portal-data-table>
        <portal-modal
          ?open=${this.confirmDelete !== null}
          heading="Delete asset?"
        >
          <p>Soft-delete asset ${this.confirmDelete?.id}? Playback will stop.</p>
          <portal-button
            variant="danger"
            @click=${(): void => {
              if (this.confirmDelete) void this.softDelete(this.confirmDelete);
            }}
          >
            Confirm delete
          </portal-button>
          <portal-button
            variant="ghost"
            @click=${(): void => {
              this.confirmDelete = null;
            }}
          >
            Cancel
          </portal-button>
        </portal-modal>
      </portal-card>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-assets": PortalAssets;
  }
}
