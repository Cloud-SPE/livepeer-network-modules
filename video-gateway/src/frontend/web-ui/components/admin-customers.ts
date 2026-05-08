import { LitElement, css, html, type TemplateResult } from "lit";
import { customElement, state } from "lit/decorators.js";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";

interface CustomerRow {
  id: string;
  email: string;
  balanceCents: number;
  reservedCents: number;
}

@customElement("admin-customers")
export class AdminCustomers extends LitElement {
  @state() private query = "";
  @state() private rows: CustomerRow[] = [];
  @state() private loading = false;
  @state() private error: string | null = null;

  private api = new ApiClient({ baseUrl: "" });

  static styles = css`
    :host { display: block; }
    .toolbar {
      display: flex;
      gap: var(--space-2);
    }
    input {
      flex: 1;
      min-height: 2.75rem;
    }
    table {
      width: 100%;
      border-collapse: collapse;
      overflow: hidden;
      border-radius: var(--radius-lg);
      background: rgba(255, 255, 255, 0.02);
    }
    th, td {
      padding: 0.8rem 0.9rem;
      text-align: left;
      border-bottom: 1px solid var(--border-1);
      font-size: var(--font-size-sm);
      vertical-align: top;
    }
    th {
      color: var(--text-3);
      font-size: var(--font-size-xs);
      font-weight: 650;
      text-transform: uppercase;
      letter-spacing: 0.1em;
    }
    tbody tr:hover {
      background: rgba(255, 255, 255, 0.025);
    }
    a {
      color: var(--accent);
      text-underline-offset: 0.18em;
    }
    .err { color: var(--danger); }
    .money {
      font-family: var(--font-mono);
      color: var(--text-1);
    }
  `;

  private async search(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const q = encodeURIComponent(this.query.trim());
      const out = await this.api.get<{ items: CustomerRow[] }>(`/admin/customers?q=${q}`);
      this.rows = out.items ?? [];
    } catch (err) {
      this.error = err instanceof Error ? err.message : "lookup_failed";
      this.rows = [];
    } finally {
      this.loading = false;
    }
  }

  private onInput(e: Event): void {
    this.query = (e.target as HTMLInputElement).value;
  }

  private onKey(e: KeyboardEvent): void {
    if (e.key === "Enter") void this.search();
  }

  render(): TemplateResult {
    return html`
      <portal-card heading="Customer Ledger">
        <portal-data-table
          heading="Customers"
          description="Search customer balances, reservations, and manual adjustment targets."
        >
        <div class="toolbar" slot="toolbar">
          <input
            placeholder="email or customer id"
            .value=${this.query}
            @input=${this.onInput}
            @keydown=${this.onKey}
          />
          <portal-button @click=${(): void => void this.search()}>Search</portal-button>
        </div>
        ${this.error ? html`<p class="err">${this.error}</p>` : ""}
        ${this.loading ? html`<p>Loading.</p>` : ""}
        <table>
          <thead>
            <tr><th>ID</th><th>Email</th><th>Balance</th><th>Reserved</th><th></th></tr>
          </thead>
          <tbody>
            ${this.rows.map(
              (r) => html`
                <tr>
                  <td><a href="#/customers/${r.id}">${r.id}</a></td>
                  <td>${r.email}</td>
                  <td class="money">$${(r.balanceCents / 100).toFixed(2)}</td>
                  <td class="money">$${(r.reservedCents / 100).toFixed(2)}</td>
                  <td>
                    <a href="#/customers/${r.id}/adjust">adjust</a>
                    &middot;
                    <a href="#/customers/${r.id}/refund">refund</a>
                  </td>
                </tr>
              `,
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
    "admin-customers": AdminCustomers;
  }
}
