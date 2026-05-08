import { LitElement, css, html, type TemplateResult } from "lit";
import { customElement, state } from "lit/decorators.js";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";

interface AssetRow {
  id: string;
  projectId: string;
  status: string;
  durationSec: number | null;
  createdAt: string;
  deletedAt: string | null;
}

@customElement("admin-assets")
export class AdminAssets extends LitElement {
  @state() private rows: AssetRow[] = [];
  @state() private includeDeleted = false;
  @state() private error: string | null = null;

  private api = new ApiClient({ baseUrl: "" });

  static styles = css`
    :host { display: block; }
    .toolbar { display: flex; gap: var(--space-3); align-items: center; }
    table { width: 100%; border-collapse: collapse; font-size: var(--font-size-sm); }
    th, td { padding: 0.75rem 0.8rem; border-bottom: 1px solid var(--border-1); text-align: left; vertical-align: top; }
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
  `;

  override async connectedCallback(): Promise<void> {
    super.connectedCallback();
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
    }
  }

  render(): TemplateResult {
    return html`
      <portal-card heading="Asset Library">
        <portal-data-table
          heading="Asset Inventory"
          description="Inspect active and soft-deleted assets across customer projects."
        >
        <div class="toolbar" slot="toolbar">
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
        ${this.error ? html`<p class="err">${this.error}</p>` : ""}
        <table>
          <thead>
            <tr><th>ID</th><th>Project</th><th>Status</th><th>Duration</th><th>Created</th><th>Deleted</th><th></th></tr>
          </thead>
          <tbody>
            ${this.rows.map(
              (r) => html`<tr class=${r.deletedAt ? "deleted" : ""}>
                <td>${r.id}</td>
                <td>${r.projectId}</td>
                <td>${r.status}</td>
                <td>${r.durationSec !== null ? r.durationSec.toFixed(1) + "s" : "-"}</td>
                <td>${r.createdAt}</td>
                <td>${r.deletedAt ?? ""}</td>
                <td>
                  <button @click=${(): void => void this.toggleDelete(r)}>
                    ${r.deletedAt ? "Restore" : "Soft-delete"}
                  </button>
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
    "admin-assets": AdminAssets;
  }
}
