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
    .toolbar { display: flex; gap: 0.75rem; align-items: center; margin-bottom: 0.75rem; }
    table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
    th, td { padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--border-1, #d4d4d8); text-align: left; }
    .deleted { color: var(--text-3, #71717a); }
    button { background: none; border: 1px solid var(--border-1, #d4d4d8); border-radius: 0.25rem; padding: 0.25rem 0.5rem; cursor: pointer; font-size: 0.75rem; }
    .err { color: #b91c1c; }
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
      <h2>Asset library</h2>
      <div class="toolbar">
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
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-assets": AdminAssets;
  }
}
