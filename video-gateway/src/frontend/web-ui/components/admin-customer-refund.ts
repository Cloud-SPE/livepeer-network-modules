import { LitElement, css, html, type TemplateResult } from "lit";
import { customElement, property, state } from "lit/decorators.js";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";

@customElement("admin-customer-refund")
export class AdminCustomerRefund extends LitElement {
  @property({ type: String }) customerId = "";
  @state() private topupId = "";
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

  private async submit(e: Event): Promise<void> {
    e.preventDefault();
    if (!this.topupId.trim() || !this.reason.trim()) {
      this.status = "err";
      this.message = "top-up id and reason are required";
      return;
    }
    this.status = "submitting";
    try {
      await this.api.post(`/admin/customers/${encodeURIComponent(this.customerId)}/refund`, {
        topup_id: this.topupId,
        amount_cents: this.amountCents,
        reason: this.reason,
      });
      this.status = "ok";
      this.message = "refund issued";
    } catch (err) {
      this.status = "err";
      this.message = err instanceof Error ? err.message : "refund_failed";
    }
  }

  render(): TemplateResult {
    return html`
      <portal-card heading="Manual Refund — ${this.customerId}">
        <portal-detail-section
          heading="Refund request"
          description="Anchor the refund to a prior top-up and record an operator-visible reason."
        >
          <p class="note">Use the exact top-up identifier when refunding previously prepaid funds.</p>
          <form @submit=${this.submit}>
            <label>
              Top-up ID to refund against
              <input
                .value=${this.topupId}
                @input=${(e: Event): void => {
                  this.topupId = (e.target as HTMLInputElement).value;
                }}
              />
            </label>
            <label>
              Amount (cents)
              <input
                type="number"
                .value=${String(this.amountCents)}
                @input=${(e: Event): void => {
                  const v = parseInt((e.target as HTMLInputElement).value, 10);
                  this.amountCents = Number.isFinite(v) ? v : 0;
                }}
              />
            </label>
            <label>
              Reason
              <textarea
                rows="3"
                .value=${this.reason}
                @input=${(e: Event): void => {
                  this.reason = (e.target as HTMLTextAreaElement).value;
                }}
              ></textarea>
            </label>
            <portal-button type="submit" ?disabled=${this.status === "submitting"}>
              ${this.status === "submitting" ? "Submitting." : "Issue refund"}
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
    "admin-customer-refund": AdminCustomerRefund;
  }
}
