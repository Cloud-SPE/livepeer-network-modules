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
    form { display: grid; gap: 0.75rem; max-width: 28rem; }
    label { display: grid; gap: 0.25rem; font-size: 0.875rem; }
    input, textarea { padding: 0.5rem; border: 1px solid var(--border-1, #d4d4d8); border-radius: 0.375rem; }
    .ok { color: #166534; }
    .err { color: #b91c1c; }
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
      <h2>Adjust balance — ${this.customerId}</h2>
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
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-customer-adjust": AdminCustomerAdjust;
  }
}
