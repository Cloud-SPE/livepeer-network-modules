import { LitElement, css, html, type TemplateResult } from "lit";
import { customElement, property, state } from "lit/decorators.js";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";

@customElement("admin-customer-adjust")
export class AdminCustomerAdjust extends LitElement {
  @property({ type: String }) customerId = "";
  @state() private amountCents = 0;
  @state() private reason = "";
  @state() private status: "idle" | "submitting" | "ok" | "err" = "idle";
  @state() private message = "";

  private api = new ApiClient({ baseUrl: "" });

  static styles = css`
    :host { display: block; }
    form { display: grid; gap: var(--space-3); max-width: 32rem; }
    label { display: grid; gap: var(--space-1); font-size: var(--font-size-sm); color: var(--text-2); font-weight: 550; }
    input, textarea { min-height: 2.8rem; padding: 0.7rem 0.85rem; border: 1px solid var(--border-1); border-radius: var(--radius-md); background: rgba(255,255,255,0.03); color: var(--text-1); }
    .ok { color: var(--success); }
    .err { color: var(--danger); }
    .note { color: var(--text-2); font-size: var(--font-size-sm); }
  `;

  private onAmount(e: Event): void {
    const v = parseInt((e.target as HTMLInputElement).value, 10);
    this.amountCents = Number.isFinite(v) ? v : 0;
  }

  private onReason(e: Event): void {
    this.reason = (e.target as HTMLTextAreaElement).value;
  }

  private async submit(e: Event): Promise<void> {
    e.preventDefault();
    if (!this.reason.trim()) {
      this.status = "err";
      this.message = "reason is required";
      return;
    }
    this.status = "submitting";
    try {
      await this.api.post(`/admin/customers/${encodeURIComponent(this.customerId)}/adjust`, {
        amount_cents: this.amountCents,
        reason: this.reason,
      });
      this.status = "ok";
      this.message = "balance adjusted";
    } catch (err) {
      this.status = "err";
      this.message = err instanceof Error ? err.message : "adjust_failed";
    }
  }

  render(): TemplateResult {
    return html`
      <portal-card heading="Adjust Balance — ${this.customerId}">
        <portal-detail-section
          heading="Manual adjustment"
          description="Credit or debit an account directly in cents. Negative amounts reduce balance."
        >
          <p class="note">Use this only for operator-driven corrections with a durable audit reason.</p>
          <form @submit=${this.submit}>
            <label>
              Amount (cents — negative to debit)
              <input type="number" .value=${String(this.amountCents)} @input=${this.onAmount} />
            </label>
            <label>
              Reason
              <textarea rows="3" .value=${this.reason} @input=${this.onReason}></textarea>
            </label>
            <portal-button type="submit" ?disabled=${this.status === "submitting"}>
              ${this.status === "submitting" ? "Submitting." : "Apply adjustment"}
            </portal-button>
            ${this.status === "ok" ? html`<p class="ok">${this.message}</p>` : ""}
            ${this.status === "err" ? html`<p class="err">${this.message}</p>` : ""}
          </form>
        </portal-detail-section>
      </portal-card>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-customer-adjust": AdminCustomerAdjust;
  }
}
