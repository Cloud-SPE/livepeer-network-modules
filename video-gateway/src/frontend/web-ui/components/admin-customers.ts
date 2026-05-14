import { html, nothing, render } from "lit";
import { ApiClient } from "@livepeer-network-modules/customer-portal-shared";
import { installAdminPageStyles } from "./admin-shared.js";

interface CustomerRow {
  id: string;
  email: string;
  tier?: string;
  status?: string;
  balanceCents: number;
  reservedCents: number;
}

export class AdminCustomers extends HTMLElement {
  private query = "";
  private rows: CustomerRow[] = [];
  private loading = false;
  private error: string | null = null;
  private creating = false;
  private createEmail = "";
  private createTier: "free" | "prepaid" = "prepaid";
  private createBalanceUsd = "0.00";
  private issuedAuthToken: string | null = null;

  private api = new ApiClient({ baseUrl: "" });

  private async search(): Promise<void> {
    this.loading = true;
    this.error = null;
    this.draw();
    try {
      const q = encodeURIComponent(this.query.trim());
      const out = await this.api.get<{ customers?: Array<Record<string, unknown>> }>(
        `/admin/customers?q=${q}`,
      );
      this.rows = (out.customers ?? []).map((row) => this.mapRow(row));
    } catch (err) {
      this.error = err instanceof Error ? err.message : "lookup_failed";
      this.rows = [];
    } finally {
      this.loading = false;
      this.draw();
    }
  }

  private onInput = (e: Event): void => {
    this.query = (e.target as HTMLInputElement).value;
  };

  private onKey = (e: KeyboardEvent): void => {
    if (e.key === "Enter") void this.search();
  };

  async connectedCallback(): Promise<void> {
    installAdminPageStyles();
    this.draw();
    await this.search();
  }

  private createCustomer = async (event: Event): Promise<void> => {
    event.preventDefault();
    this.creating = true;
    this.error = null;
    this.issuedAuthToken = null;
    this.draw();
    try {
      const initialBalance = Math.round(Number(this.createBalanceUsd) * 100);
      const out = await this.api.post<{
        auth_token?: string;
        customer?: { id: string };
      }>("/admin/customers", {
        email: this.createEmail,
        tier: this.createTier,
        initial_balance_usd_cents: Number.isFinite(initialBalance) ? initialBalance : 0,
      });
      this.issuedAuthToken = out.auth_token ?? null;
      this.createEmail = "";
      this.createTier = "prepaid";
      this.createBalanceUsd = "0.00";
      await this.search();
      if (out.customer?.id) {
        window.location.hash = `#/customers/${out.customer.id}`;
      }
    } catch (err) {
      this.error = err instanceof Error ? err.message : "create_failed";
    } finally {
      this.creating = false;
      this.draw();
    }
  };

  private mapRow(row: Record<string, unknown>): CustomerRow {
    return {
      id: String(row["id"] ?? ""),
      email: String(row["email"] ?? ""),
      tier: row["tier"] ? String(row["tier"]) : undefined,
      status: row["status"] ? String(row["status"]) : undefined,
      balanceCents: parseInt(String(row["balance_usd_cents"] ?? "0"), 10) || 0,
      reservedCents: parseInt(String(row["reserved_usd_cents"] ?? "0"), 10) || 0,
    };
  }

  private draw(): void {
    render(
      html`
      <portal-card heading="Customer Ledger">
        <portal-data-table
          heading="Customers"
          description="Create customers, inspect balances, and jump into token or API-key management."
        >
        <form class="video-admin-page-grid" @submit=${this.createCustomer}>
          ${this.issuedAuthToken
            ? html`
                <div class="video-admin-page-plaintext">
                  Initial customer auth token. Save it now; it is only shown once:
                  <div>${this.issuedAuthToken}</div>
                </div>
              `
            : nothing}
          <div class="video-admin-page-grid video-admin-page-grid--fit">
            <label class="video-admin-page-field">
              Email
              <input
                type="email"
                required
                .value=${this.createEmail}
                @input=${(event: Event): void => {
                  this.createEmail = (event.target as HTMLInputElement).value;
                }}
              />
            </label>
            <label class="video-admin-page-field">
              Tier
              <select
                .value=${this.createTier}
                @change=${(event: Event): void => {
                  this.createTier = (event.target as HTMLSelectElement).value as "free" | "prepaid";
                }}
              >
                <option value="prepaid">prepaid</option>
                <option value="free">free</option>
              </select>
            </label>
            <label class="video-admin-page-field">
              Initial balance (USD)
              <input
                type="number"
                min="0"
                step="0.01"
                .value=${this.createBalanceUsd}
                @input=${(event: Event): void => {
                  this.createBalanceUsd = (event.target as HTMLInputElement).value;
                }}
              />
            </label>
          </div>
          <div class="video-admin-page-actions">
            <portal-button type="submit" ?disabled=${this.creating}>
              ${this.creating ? "Creating." : "Create customer"}
            </portal-button>
          </div>
        </form>
        <div class="video-admin-page-toolbar video-admin-page-toolbar--grow" slot="toolbar">
          <input
            placeholder="email or customer id"
            .value=${this.query}
            @input=${this.onInput}
            @keydown=${this.onKey}
          />
          <portal-button @click=${(): void => void this.search()}>Search</portal-button>
        </div>
        ${this.error ? html`<p class="video-admin-page-error">${this.error}</p>` : nothing}
        ${this.loading ? html`<p>Loading.</p>` : nothing}
        <table class="video-admin-page-table">
          <thead>
            <tr><th>ID</th><th>Email</th><th>Tier</th><th>Status</th><th>Balance</th><th>Reserved</th><th></th></tr>
          </thead>
          <tbody>
            ${this.rows.map(
              (r) => html`
                <tr>
                  <td><a class="video-admin-page-link" href="#/customers/${r.id}">${r.id}</a></td>
                  <td>${r.email}</td>
                  <td>${r.tier ?? "—"}</td>
                  <td>${r.status ?? "—"}</td>
                  <td class="video-admin-page-money">$${(r.balanceCents / 100).toFixed(2)}</td>
                  <td class="video-admin-page-money">$${(r.reservedCents / 100).toFixed(2)}</td>
                  <td>
                    <a class="video-admin-page-link" href="#/customers/${r.id}/adjust">adjust</a>
                    &middot;
                    <a class="video-admin-page-link" href="#/customers/${r.id}/refund">refund</a>
                  </td>
                </tr>
              `,
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

if (!customElements.get("admin-customers")) {
  customElements.define("admin-customers", AdminCustomers);
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-customers": AdminCustomers;
  }
}
