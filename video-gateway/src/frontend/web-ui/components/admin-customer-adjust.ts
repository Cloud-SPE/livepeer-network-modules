import { html, nothing, render } from "lit";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";
import { installAdminPageStyles } from "./admin-shared.js";

export class AdminCustomerAdjust extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["customerid"];
  }

  private amountCents = 0;
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
      this.draw();
      return;
    }
    this.status = "submitting";
    this.draw();
    try {
      await this.api.post(`/admin/customers/${encodeURIComponent(this.customerId)}/balance`, {
        delta_usd_cents: this.amountCents,
        reason: this.reason,
      });
      this.status = "ok";
      this.message = "balance adjusted";
    } catch (err) {
      this.status = "err";
      this.message = err instanceof Error ? err.message : "adjust_failed";
    }
    this.draw();
  }

  private draw(): void {
    render(
      html`
      <portal-card heading="Adjust Balance — ${this.customerId}">
        <portal-detail-section
          heading="Manual adjustment"
          description="Credit or debit an account directly in cents. Negative amounts reduce balance."
        >
          <p class="video-admin-page-note">Use this only for operator-driven corrections with a durable audit reason.</p>
          <form @submit=${this.submit} class="video-admin-page-form">
            <label class="video-admin-page-field">
              Amount (cents — negative to debit)
              <input type="number" .value=${String(this.amountCents)} @input=${this.onAmount} />
            </label>
            <label class="video-admin-page-field">
              Reason
              <textarea rows="3" .value=${this.reason} @input=${this.onReason}></textarea>
            </label>
            <portal-button type="submit" ?disabled=${this.status === "submitting"}>
              ${this.status === "submitting" ? "Submitting." : "Apply adjustment"}
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

if (!customElements.get("admin-customer-adjust")) {
  customElements.define("admin-customer-adjust", AdminCustomerAdjust);
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-customer-adjust": AdminCustomerAdjust;
  }
}
