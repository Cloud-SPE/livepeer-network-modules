import { html, render, nothing } from "lit";
import { ApiClient } from "@livepeer-network-modules/customer-portal-shared";
import { installAdminPageStyles } from "./admin-shared.js";

interface TopupRow {
  id: string;
  customerId: string;
  stripeSessionId: string;
  amountUsdCents: number;
  status: string;
  createdAt: string;
  refundedAt: string | null;
}

export class AdminTopups extends HTMLElement {
  private rows: TopupRow[] = [];
  private error: string | null = null;
  private api = new ApiClient({ baseUrl: "" });

  async connectedCallback(): Promise<void> {
    installAdminPageStyles();
    this.draw();
    try {
      const out = await this.api.get<{ topups?: Array<Record<string, unknown>> }>("/admin/topups?limit=50");
      this.rows = (out.topups ?? []).map((row) => ({
        id: String(row["id"] ?? ""),
        customerId: String(row["customer_id"] ?? ""),
        stripeSessionId: String(row["stripe_session_id"] ?? ""),
        amountUsdCents: parseInt(String(row["amount_usd_cents"] ?? "0"), 10) || 0,
        status: String(row["status"] ?? ""),
        createdAt: String(row["created_at"] ?? ""),
        refundedAt: row["refunded_at"] ? String(row["refunded_at"]) : null,
      }));
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
    this.draw();
  }

  private draw(): void {
    render(
      html`
      <portal-card heading="Top-ups">
        <portal-data-table
          heading="Prepaid balance events"
          description="Recent customer top-ups and their Stripe settlement lifecycle."
        >
          ${this.error ? html`<p class="video-admin-page-error">${this.error}</p>` : nothing}
          <table class="video-admin-page-table">
            <thead><tr><th>ID</th><th>Customer</th><th>Stripe session</th><th>Amount</th><th>Status</th><th>Created</th></tr></thead>
            <tbody>
              ${this.rows.map(
                (row) => html`<tr>
                  <td>${row.id}</td>
                  <td><a class="video-admin-page-link" href="#/customers/${row.customerId}">${row.customerId}</a></td>
                  <td>${row.stripeSessionId}</td>
                  <td class="video-admin-page-money">$${(row.amountUsdCents / 100).toFixed(2)}</td>
                  <td>${row.status}</td>
                  <td>${row.createdAt}</td>
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

if (!customElements.get("admin-topups")) {
  customElements.define("admin-topups", AdminTopups);
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-topups": AdminTopups;
  }
}
