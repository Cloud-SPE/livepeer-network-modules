import { LitElement, css, html, type TemplateResult } from "lit";
import { customElement, property, state } from "lit/decorators.js";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";

interface CustomerSummary {
  id: string;
  email: string;
  balanceCents: number;
  reservedCents: number;
  topups: { id: string; amountCents: number; createdAt: string; status: string }[];
  reservations: { id: string; amountCents: number; createdAt: string; capability: string }[];
  audit: { ts: string; actor: string; action: string; detail: string }[];
}

@customElement("admin-customer-detail")
export class AdminCustomerDetail extends LitElement {
  @property({ type: String }) customerId = "";
  @state() private data: CustomerSummary | null = null;
  @state() private error: string | null = null;

  private api = new ApiClient({ baseUrl: "" });

  static styles = css`
    :host { display: block; }
    h2 { margin-top: 0; }
    section { margin-top: 1rem; }
    table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
    th, td { padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--border-1, #d4d4d8); text-align: left; }
    .actions { display: flex; gap: 0.5rem; }
    a.btn { padding: 0.4rem 0.7rem; border: 1px solid var(--border-1, #d4d4d8); border-radius: 0.375rem; text-decoration: none; color: var(--text-1, #18181b); font-size: 0.875rem; }
    .err { color: #b91c1c; }
  `;

  override async connectedCallback(): Promise<void> {
    super.connectedCallback();
    await this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      this.data = await this.api.get<CustomerSummary>(
        `/admin/customers/${encodeURIComponent(this.customerId)}`,
      );
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
  }

  render(): TemplateResult {
    if (this.error) return html`<p class="err">${this.error}</p>`;
    if (!this.data) return html`<p>Loading.</p>`;
    const d = this.data;
    return html`
      <h2>Customer ${d.id}</h2>
      <p>${d.email}</p>
      <p>
        Balance: <b>$${(d.balanceCents / 100).toFixed(2)}</b>
        &middot;
        Reserved: <b>$${(d.reservedCents / 100).toFixed(2)}</b>
      </p>
      <div class="actions">
        <a class="btn" href="#/customers/${d.id}/adjust">Adjust balance</a>
        <a class="btn" href="#/customers/${d.id}/refund">Refund</a>
      </div>

      <section>
        <h3>Top-ups</h3>
        <table>
          <thead><tr><th>ID</th><th>Amount</th><th>Status</th><th>When</th></tr></thead>
          <tbody>
            ${d.topups.map(
              (t) => html`<tr>
                <td>${t.id}</td>
                <td>$${(t.amountCents / 100).toFixed(2)}</td>
                <td>${t.status}</td>
                <td>${t.createdAt}</td>
              </tr>`,
            )}
          </tbody>
        </table>
      </section>

      <section>
        <h3>Active reservations</h3>
        <table>
          <thead><tr><th>ID</th><th>Amount</th><th>Capability</th><th>Since</th></tr></thead>
          <tbody>
            ${d.reservations.map(
              (r) => html`<tr>
                <td>${r.id}</td>
                <td>$${(r.amountCents / 100).toFixed(2)}</td>
                <td>${r.capability}</td>
                <td>${r.createdAt}</td>
              </tr>`,
            )}
          </tbody>
        </table>
      </section>

      <section>
        <h3>Audit</h3>
        <table>
          <thead><tr><th>When</th><th>Actor</th><th>Action</th><th>Detail</th></tr></thead>
          <tbody>
            ${d.audit.map(
              (a) => html`<tr>
                <td>${a.ts}</td>
                <td>${a.actor}</td>
                <td>${a.action}</td>
                <td>${a.detail}</td>
              </tr>`,
            )}
          </tbody>
        </table>
      </section>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-customer-detail": AdminCustomerDetail;
  }
}
