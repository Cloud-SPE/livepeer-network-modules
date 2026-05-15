// Operator login. Stores the admin token + actor in sessionStorage;
// every subsequent admin request sends both as headers (consumed by
// customer-portal's StaticAdminTokenAuthResolver on the backend).

import { writeCreds, readCreds } from "../lib/api.js";

export class AdminDaydreamLogin extends HTMLElement {
  private error: string | null = null;

  connectedCallback(): void {
    this.render();
  }

  private render(): void {
    const existing = readCreds();
    this.innerHTML = `
      <portal-card heading="Operator sign-in">
        ${existing ? `<p>Currently signed in as <strong>${escapeHtml(existing.actor)}</strong>.</p>` : ""}
        <portal-input id="dd-admin-actor" label="Actor (your name)" value="${escapeAttr(existing?.actor ?? "")}"></portal-input>
        <portal-input id="dd-admin-token" label="Admin token" type="password"></portal-input>
        ${this.error ? `<p class="portal-form-error">${escapeHtml(this.error)}</p>` : ""}
        <portal-action-row>
          <portal-button id="dd-admin-save">Save</portal-button>
        </portal-action-row>
      </portal-card>`;
    this.querySelector("#dd-admin-save")?.addEventListener("click", () => {
      const actor = (
        this.querySelector<HTMLInputElement>("#dd-admin-actor")?.value ?? ""
      ).trim();
      const token = (
        this.querySelector<HTMLInputElement>("#dd-admin-token")?.value ?? ""
      ).trim();
      if (!actor || !token) {
        this.error = "Both actor and token are required.";
        this.render();
        return;
      }
      writeCreds({ actor, token });
      this.error = null;
      this.dispatchEvent(
        new CustomEvent("daydream-admin-authed", { bubbles: true }),
      );
    });
  }
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function escapeAttr(s: string): string {
  return escapeHtml(s).replace(/'/g, "&#39;");
}

customElements.define("admin-daydream-login", AdminDaydreamLogin);
