import { html, render, nothing } from "lit";
import { ApiClient } from "@livepeer-network-modules/customer-portal-shared";
import { installAdminPageStyles } from "./admin-shared.js";

interface AuditRow {
  ts: string;
  actor: string;
  action: string;
  detail: string;
}

export class AdminAudit extends HTMLElement {
  private rows: AuditRow[] = [];
  private error: string | null = null;
  private api = new ApiClient({ baseUrl: "" });

  async connectedCallback(): Promise<void> {
    installAdminPageStyles();
    this.draw();
    try {
      const out = await this.api.get<{ events?: Array<Record<string, unknown>> }>("/admin/audit?limit=100");
      this.rows = (out.events ?? []).map((row) => ({
        ts: String(row["ts"] ?? ""),
        actor: String(row["actor"] ?? ""),
        action: String(row["action"] ?? ""),
        detail: String(row["detail"] ?? ""),
      }));
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
    this.draw();
  }

  private draw(): void {
    render(
      html`
      <portal-card heading="Audit">
        <portal-data-table
          heading="Audit trail"
          description="Recent operator-visible customer and billing actions."
        >
          ${this.error ? html`<p class="video-admin-page-error">${this.error}</p>` : nothing}
          <table class="video-admin-page-table">
            <thead><tr><th>When</th><th>Actor</th><th>Action</th><th>Detail</th></tr></thead>
            <tbody>
              ${this.rows.map(
                (row) => html`<tr>
                  <td>${row.ts}</td>
                  <td>${row.actor}</td>
                  <td>${row.action}</td>
                  <td>${row.detail}</td>
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

if (!customElements.get("admin-audit")) {
  customElements.define("admin-audit", AdminAudit);
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-audit": AdminAudit;
  }
}
