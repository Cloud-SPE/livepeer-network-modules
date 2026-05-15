// Public, unauthenticated waitlist signup form. The portal app shell
// shows this when the user is not logged in. After successful submit
// we drop the user on a status page that polls /portal/waitlist/status
// until admin approval — at which point the user is told to expect
// the API key out-of-band from the operator.

import { DaydreamPortalApi } from "../lib/api.js";

export class PortalDaydreamWaitlist extends HTMLElement {
  private api = new DaydreamPortalApi();
  private submitting = false;
  private submittedEmail: string | null = null;
  private lastStatus: string | null = null;
  private error: string | null = null;
  private pollTimer: number | null = null;

  connectedCallback(): void {
    this.render();
  }

  disconnectedCallback(): void {
    if (this.pollTimer !== null) window.clearInterval(this.pollTimer);
  }

  private render(): void {
    if (this.submittedEmail) {
      this.innerHTML = `
        <portal-card heading="You're on the list">
          <p>We've recorded <strong>${escapeHtml(this.submittedEmail)}</strong>.</p>
          <p>An operator will review your request. When you're approved
          you'll receive your API key out-of-band — keep an eye out.</p>
          <p>Current status: <strong>${escapeHtml(this.lastStatus ?? "pending")}</strong></p>
          <portal-button id="waitlist-back">Back to login</portal-button>
        </portal-card>`;
      const back = this.querySelector("#waitlist-back");
      back?.addEventListener("click", () => {
        location.hash = "#/login";
      });
      this.startPolling();
      return;
    }

    this.innerHTML = `
      <portal-card heading="Request access">
        <p>Daydream-portal is invite-gated. Drop your email and an
        optional note; an operator will review and issue you an API
        key.</p>
        <form id="waitlist-form" novalidate>
          <portal-input
            name="email"
            type="email"
            label="Email"
            placeholder="you@example.com"
            required>
          </portal-input>
          <portal-input
            name="display_name"
            label="Display name (optional)"
            placeholder="How should we address you?">
          </portal-input>
          <portal-input
            name="reason"
            label="Why are you applying? (optional)"
            placeholder="Briefly tell us what you want to build">
          </portal-input>
          ${this.error ? `<p class="portal-form-error">${escapeHtml(this.error)}</p>` : ""}
          <portal-button id="waitlist-submit" ${this.submitting ? "disabled" : ""}>
            ${this.submitting ? "Submitting…" : "Request access"}
          </portal-button>
        </form>
      </portal-card>`;
    this.querySelector("#waitlist-submit")?.addEventListener("click", (ev) => {
      ev.preventDefault();
      void this.onSubmit();
    });
  }

  private async onSubmit(): Promise<void> {
    if (this.submitting) return;
    const form = this.querySelector<HTMLFormElement>("#waitlist-form");
    if (!form) return;
    const email = readInput(form, "email").trim().toLowerCase();
    const displayName = readInput(form, "display_name").trim() || undefined;
    const reason = readInput(form, "reason").trim() || undefined;
    if (!email || !/.+@.+\..+/.test(email)) {
      this.error = "Enter a valid email.";
      this.render();
      return;
    }
    this.submitting = true;
    this.error = null;
    this.render();
    try {
      await this.api.signupWaitlist({
        email,
        display_name: displayName,
        reason,
      });
      this.submittedEmail = email;
      this.lastStatus = "pending";
    } catch (err) {
      this.error = `Submission failed: ${err instanceof Error ? err.message : String(err)}`;
    } finally {
      this.submitting = false;
      this.render();
    }
  }

  private startPolling(): void {
    if (this.pollTimer !== null || !this.submittedEmail) return;
    const tick = async () => {
      if (!this.submittedEmail) return;
      try {
        const res = await this.api.waitlistStatus(this.submittedEmail);
        if (res.status !== this.lastStatus) {
          this.lastStatus = res.status;
          this.render();
        }
      } catch {
        // Network blips are tolerable; we'll try again next tick.
      }
    };
    this.pollTimer = window.setInterval(() => void tick(), 15_000);
    void tick();
  }
}

function readInput(form: HTMLFormElement, name: string): string {
  const el = form.querySelector(`[name="${name}"]`) as
    | (HTMLElement & { value?: string })
    | null;
  return el?.value ?? "";
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

customElements.define("portal-daydream-waitlist", PortalDaydreamWaitlist);
