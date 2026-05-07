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
    .toolbar { display: flex; gap: 0.5rem; margin-bottom: 0.75rem; }
    input { padding: 0.5rem; border: 1px solid var(--border-1, #d4d4d8); border-radius: 0.375rem; flex: 1; }
    table { width: 100%; border-collapse: collapse; }
    th, td { padding: 0.5rem 0.75rem; text-align: left; border-bottom: 1px solid var(--border-1, #d4d4d8); font-size: 0.875rem; }
    th { font-weight: 600; }
    a { color: var(--accent-1, #2563eb); }
    .err { color: #b91c1c; }
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
      <div class="toolbar">
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
                <td>$${(r.balanceCents / 100).toFixed(2)}</td>
                <td>$${(r.reservedCents / 100).toFixed(2)}</td>
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
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-customers": AdminCustomers;
  }
}
