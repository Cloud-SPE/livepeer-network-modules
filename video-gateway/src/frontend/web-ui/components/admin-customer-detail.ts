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
    table { width: 100%; border-collapse: collapse; font-size: var(--font-size-sm); }
    th, td { padding: 0.75rem 0.8rem; border-bottom: 1px solid var(--border-1); text-align: left; }
    th {
      color: var(--text-3);
      font-size: var(--font-size-xs);
      font-weight: 650;
      text-transform: uppercase;
      letter-spacing: 0.1em;
    }
    tbody tr:hover { background: rgba(255, 255, 255, 0.02); }
    .err { color: var(--danger); }
    .summary { display: grid; gap: var(--space-2); margin-bottom: var(--space-4); }
    .money { font-family: var(--font-mono); color: var(--text-1); }
    .dim { color: var(--text-3); }
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
      <portal-card heading="Customer ${d.id}">
        <div class="summary">
          <p>${d.email}</p>
          <p>
            Balance: <b class="money">$${(d.balanceCents / 100).toFixed(2)}</b>
            &middot;
            Reserved: <b class="money">$${(d.reservedCents / 100).toFixed(2)}</b>
          </p>
        </div>
        <portal-action-row>
          <portal-button
            variant="ghost"
            @click=${(): void => {
              window.location.hash = `#/customers/${d.id}/adjust`;
            }}
          >
            Adjust balance
          </portal-button>
          <portal-button
            variant="danger"
            @click=${(): void => {
              window.location.hash = `#/customers/${d.id}/refund`;
            }}
          >
            Refund
          </portal-button>
        </portal-action-row>

        <portal-detail-section
          heading="Top-ups"
          description="Ledger of prepaid balance additions and their settlement status."
        >
          <table>
            <thead><tr><th>ID</th><th>Amount</th><th>Status</th><th>When</th></tr></thead>
            <tbody>
              ${d.topups.map(
                (t) => html`<tr>
                  <td>${t.id}</td>
                  <td class="money">$${(t.amountCents / 100).toFixed(2)}</td>
                  <td>
                    <portal-status-pill variant=${t.status === "completed" ? "success" : t.status === "failed" ? "danger" : "info"}>
                      ${t.status}
                    </portal-status-pill>
                  </td>
                  <td>${t.createdAt}</td>
                </tr>`,
              )}
            </tbody>
          </table>
        </portal-detail-section>

        <portal-detail-section
          heading="Active reservations"
          description="Funds currently reserved against outstanding workload commitments."
        >
          <table>
            <thead><tr><th>ID</th><th>Amount</th><th>Capability</th><th>Since</th></tr></thead>
            <tbody>
              ${d.reservations.map(
                (r) => html`<tr>
                  <td>${r.id}</td>
                  <td class="money">$${(r.amountCents / 100).toFixed(2)}</td>
                  <td>${r.capability}</td>
                  <td>${r.createdAt}</td>
                </tr>`,
              )}
            </tbody>
          </table>
        </portal-detail-section>

        <portal-detail-section
          heading="Audit"
          description="Operator-visible balance, refund, and customer-account history."
        >
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
        </portal-detail-section>
      </portal-card>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-customer-detail": AdminCustomerDetail;
  }
}
