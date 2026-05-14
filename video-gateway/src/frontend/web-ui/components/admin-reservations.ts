import { html, render, nothing } from "lit";
import { ApiClient } from "@livepeer-network-modules/customer-portal-shared";
import { installAdminPageStyles } from "./admin-shared.js";

interface ReservationRow {
  id: string;
  customerId: string;
  kind: string;
  state: string;
  capability: string | null;
  model: string | null;
  amountUsdCents: number | null;
  committedUsdCents: number | null;
  refundedUsdCents: number | null;
  createdAt: string;
  resolvedAt: string | null;
}

export class AdminReservations extends HTMLElement {
  private rows: ReservationRow[] = [];
  private error: string | null = null;
  private api = new ApiClient({ baseUrl: "" });

  async connectedCallback(): Promise<void> {
    installAdminPageStyles();
    this.draw();
    try {
      const out = await this.api.get<{ reservations?: Array<Record<string, unknown>> }>("/admin/reservations?limit=50");
      this.rows = (out.reservations ?? []).map((row) => {
        const parseMaybe = (key: string): number | null => {
          const value = row[key];
          if (value === null || value === undefined) return null;
          const parsed = parseInt(String(value), 10);
          return Number.isFinite(parsed) ? parsed : null;
        };
        return {
          id: String(row["id"] ?? ""),
          customerId: String(row["customer_id"] ?? ""),
          kind: String(row["kind"] ?? ""),
          state: String(row["state"] ?? ""),
          capability: row["capability"] ? String(row["capability"]) : null,
          model: row["model"] ? String(row["model"]) : null,
          amountUsdCents: parseMaybe("amount_usd_cents"),
          committedUsdCents: parseMaybe("committed_usd_cents"),
          refundedUsdCents: parseMaybe("refunded_usd_cents"),
          createdAt: String(row["created_at"] ?? ""),
          resolvedAt: row["resolved_at"] ? String(row["resolved_at"]) : null,
        };
      });
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
    this.draw();
  }

  private draw(): void {
    const fmt = (value: number | null): string =>
      value === null ? "—" : `$${(value / 100).toFixed(2)}`;
    render(
      html`
      <portal-card heading="Reservations">
        <portal-data-table
          heading="Customer reservation ledger"
          description="Reserved, committed, and refunded customer funds by work item."
        >
          ${this.error ? html`<p class="video-admin-page-error">${this.error}</p>` : nothing}
          <table class="video-admin-page-table">
            <thead><tr><th>ID</th><th>Customer</th><th>Kind</th><th>State</th><th>Capability</th><th>Model</th><th>Amount</th><th>Committed</th><th>Refunded</th></tr></thead>
            <tbody>
              ${this.rows.map(
                (row) => html`<tr>
                  <td>${row.id}</td>
                  <td><a class="video-admin-page-link" href="#/customers/${row.customerId}">${row.customerId}</a></td>
                  <td>${row.kind}</td>
                  <td>${row.state}</td>
                  <td>${row.capability ?? "—"}</td>
                  <td>${row.model ?? "—"}</td>
                  <td class="video-admin-page-money">${fmt(row.amountUsdCents)}</td>
                  <td class="video-admin-page-money">${fmt(row.committedUsdCents)}</td>
                  <td class="video-admin-page-money">${fmt(row.refundedUsdCents)}</td>
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

if (!customElements.get("admin-reservations")) {
  customElements.define("admin-reservations", AdminReservations);
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-reservations": AdminReservations;
  }
}
