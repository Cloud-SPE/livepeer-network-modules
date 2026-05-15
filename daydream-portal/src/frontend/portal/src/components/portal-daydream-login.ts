// Sign-in form for daydream-portal. customer-portal's <portal-login>
// widget expects a UI auth token directly; we use the API key
// instead, since that's what the operator hands the user out-of-band.
// On submit we POST /portal/login-by-key, store the returned UI token
// in session storage, and redirect to the playground.

import { writeSession } from "@livepeer-network-modules/customer-portal-shared";
import { DaydreamPortalApi } from "../lib/api.js";

export class PortalDaydreamLogin extends HTMLElement {
  private api = new DaydreamPortalApi();
  private submitting = false;
  private error: string | null = null;

  connectedCallback(): void {
    this.render();
  }

  private render(): void {
    this.innerHTML = `
      <portal-card heading="Sign in">
        <p>Paste the API key your operator issued you.</p>
        <portal-input id="dd-login-actor" label="Display name"
          placeholder="What should we call you?"></portal-input>
        <portal-input id="dd-login-key" label="API key" type="password"
          placeholder="sk-live-…"></portal-input>
        ${this.error ? `<p class="portal-form-error">${escapeHtml(this.error)}</p>` : ""}
        <portal-action-row>
          <portal-button id="dd-login-submit" ${this.submitting ? "disabled" : ""}>
            ${this.submitting ? "Signing in…" : "Sign in"}
          </portal-button>
        </portal-action-row>
      </portal-card>`;
    this.querySelector("#dd-login-submit")?.addEventListener("click", () => {
      void this.onSubmit();
    });
  }

  private async onSubmit(): Promise<void> {
    if (this.submitting) return;
    const actor = (
      this.querySelector<HTMLInputElement>("#dd-login-actor")?.value ?? ""
    ).trim();
    const apiKey = (
      this.querySelector<HTMLInputElement>("#dd-login-key")?.value ?? ""
    ).trim();
    if (!actor || !apiKey) {
      this.error = "Both display name and API key are required.";
      this.render();
      return;
    }
    this.submitting = true;
    this.error = null;
    this.render();
    try {
      const res = await this.api.loginByKey({ api_key: apiKey, actor });
      writeSession({
        token: res.auth_token,
        actor,
        customerId: res.customer.id,
        email: res.customer.email,
      });
      location.hash = "#/playground";
      // Force the shell to redraw its nav.
      window.dispatchEvent(new StorageEvent("storage"));
    } catch (err) {
      this.error = `Sign-in failed: ${err instanceof Error ? err.message : String(err)}`;
      this.submitting = false;
      this.render();
    }
  }
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

customElements.define("portal-daydream-login", PortalDaydreamLogin);
