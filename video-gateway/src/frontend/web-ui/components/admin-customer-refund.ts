import { html, nothing, render } from "lit";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";
import { installAdminPageStyles } from "./admin-shared.js";

export class AdminCustomerRefund extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["customerid"];
  }

  private stripeSessionId = "";
  private reason = "";
  private status: "idle" | "submitting" | "ok" | "err" = "idle";
  private message = "";

  private api = new ApiClient({ baseUrl: "" });

  connectedCallback(): void {
    installAdminPageStyles();
    this.draw();
  }

  attributeChangedCallback(): void {
    if (this.isConnected) {
      this.draw();
    }
  }

  get customerId(): string {
    return this.getAttribute("customerId") ?? "";
  }

  set customerId(value: string) {
    this.setAttribute("customerId", value);
  }

  private submit = async (e: Event): Promise<void> => {
    e.preventDefault();
    if (!this.stripeSessionId.trim() || !this.reason.trim()) {
      this.status = "err";
      this.message = "Stripe session id and reason are required";
      this.draw();
      return;
    }
    this.status = "submitting";
    this.draw();
    try {
      await this.api.post(`/admin/customers/${encodeURIComponent(this.customerId)}/refund`, {
        stripe_session_id: this.stripeSessionId,
        reason: this.reason,
      });
      this.status = "ok";
      this.message = "refund issued";
    } catch (err) {
      this.status = "err";
      this.message = err instanceof Error ? err.message : "refund_failed";
    }
    this.draw();
  };

  private draw(): void {
    render(
      html`
      <portal-card heading="Manual Refund — ${this.customerId}">
        <portal-detail-section
          heading="Refund request"
          description="Refund a settled Stripe top-up and record an operator-visible reason."
        >
          <p class="video-admin-page-note">Use the Stripe session id from the customer top-up ledger when refunding prepaid funds.</p>
          <form @submit=${this.submit} class="video-admin-page-form">
            <label class="video-admin-page-field">
              Stripe session id
              <input
                .value=${this.stripeSessionId}
                @input=${(e: Event): void => {
                  this.stripeSessionId = (e.target as HTMLInputElement).value;
                }}
              />
            </label>
            <label class="video-admin-page-field">
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
            ${this.status === "ok" ? html`<p class="video-admin-page-ok">${this.message}</p>` : nothing}
            ${this.status === "err" ? html`<p class="video-admin-page-error">${this.message}</p>` : nothing}
          </form>
        </portal-detail-section>
      </portal-card>
      `,
      this,
    );
  }
}

if (!customElements.get("admin-customer-refund")) {
  customElements.define("admin-customer-refund", AdminCustomerRefund);
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-customer-refund": AdminCustomerRefund;
  }
}
