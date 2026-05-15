// Operator-wide usage. One endpoint, three tiles. No per-customer
// breakdown here yet — that lives in customer-portal's admin engine
// (/admin/customers/*) which the daydream-portal SPA delegates to via
// its own UI link.

import { DaydreamAdminApi, type AdminUsageSummary } from "../lib/api.js";

export class AdminDaydreamUsage extends HTMLElement {
  private api = new DaydreamAdminApi();
  private summary: AdminUsageSummary | null = null;
  private loading = true;
  private error: string | null = null;

  connectedCallback(): void {
    this.render();
    void this.load();
  }

  private async load(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      this.summary = await this.api.usageSummary();
    } catch (err) {
      this.error = `Could not load usage: ${err instanceof Error ? err.message : String(err)}`;
    } finally {
      this.loading = false;
      this.render();
    }
  }

  private render(): void {
    this.innerHTML = `
      <portal-card heading="Operator usage">
        ${this.loading ? "<p>Loading…</p>" : ""}
        ${this.error ? `<p class="portal-form-error">${escapeHtml(this.error)}</p>` : ""}
        ${
          this.summary
            ? `<div style="display:flex;gap:1rem;flex-wrap:wrap">
                 <portal-metric-tile label="Sessions (30d)" value="${this.summary.total_sessions}"></portal-metric-tile>
                 <portal-metric-tile label="Unique customers" value="${this.summary.unique_customers}"></portal-metric-tile>
                 <portal-metric-tile label="Total seconds" value="${this.summary.total_seconds}"></portal-metric-tile>
                 <portal-metric-tile label="Total minutes" value="${Math.round(this.summary.total_seconds / 60)}"></portal-metric-tile>
               </div>`
            : ""
        }
      </portal-card>`;
  }
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

customElements.define("admin-daydream-usage", AdminDaydreamUsage);
