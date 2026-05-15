// Admin shell. Three routes (login / signups / usage) — anything
// heavier (per-customer drill-down, manual key issue) is intentionally
// delegated to customer-portal's own admin engine routes, accessible
// via the link in the footer.

import { HashRouter } from "@livepeer-network-modules/customer-portal-shared";
import {
  readCreds,
  clearCreds,
} from "../lib/api.js";

const NAV = [
  { href: "#/signups", label: "Signups" },
  { href: "#/usage", label: "Usage" },
];

export class AdminDaydreamApp extends HTMLElement {
  private router = new HashRouter();
  private root!: HTMLElement;

  connectedCallback(): void {
    this.renderShell();
    this.wireRoutes();
    this.router.start();
    window.addEventListener("hashchange", () => this.renderNav());
    this.addEventListener("daydream-admin-authed", () => {
      this.renderNav();
      location.hash = "#/signups";
    });
  }

  private renderShell(): void {
    this.innerHTML = `
      <portal-layout>
        <header slot="header"
          style="display:flex;justify-content:space-between;align-items:center;padding:1rem">
          <strong>Daydream Admin</strong>
          <nav class="admin-daydream-app__nav"></nav>
        </header>
        <main id="admin-daydream-app-root"
          style="padding:1rem;max-width:1100px;margin:0 auto"></main>
      </portal-layout>`;
    this.root = this.querySelector<HTMLElement>("#admin-daydream-app-root")!;
    this.renderNav();
  }

  private renderNav(): void {
    const nav = this.querySelector<HTMLElement>(".admin-daydream-app__nav");
    if (!nav) return;
    const creds = readCreds();
    if (!creds) {
      nav.innerHTML = "";
      return;
    }
    nav.innerHTML =
      NAV.map(
        (i) => `<a href="${i.href}" style="margin-left:1rem">${i.label}</a>`,
      ).join("") +
      ` <span style="margin-left:1rem;opacity:.7">${escapeHtml(creds.actor)}</span>` +
      ` <portal-button id="dd-admin-signout" variant="ghost">Sign out</portal-button>`;
    nav.querySelector("#dd-admin-signout")?.addEventListener("click", () => {
      clearCreds();
      this.renderNav();
      location.hash = "#/login";
    });
  }

  private wireRoutes(): void {
    this.router
      .add("/login", () => {
        this.root.innerHTML = "<admin-daydream-login></admin-daydream-login>";
      })
      .add("/signups", () => {
        if (!this.requireAuth()) return;
        this.root.innerHTML = "<admin-daydream-signups></admin-daydream-signups>";
      })
      .add("/usage", () => {
        if (!this.requireAuth()) return;
        this.root.innerHTML = "<admin-daydream-usage></admin-daydream-usage>";
      });

    if (!location.hash || location.hash === "#" || location.hash === "#/") {
      location.hash = readCreds() ? "#/signups" : "#/login";
    }
  }

  private requireAuth(): boolean {
    if (readCreds()) return true;
    location.hash = "#/login";
    return false;
  }
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

customElements.define("admin-daydream-app", AdminDaydreamApp);
