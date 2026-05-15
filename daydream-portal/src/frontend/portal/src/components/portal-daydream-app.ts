// Top-level portal shell. Owns the layout and dispatches routes to
// the right page component. Login state comes from session-storage
// (the customer-portal-shared session helper); the shell shows the
// signed-in nav when a token is present, otherwise routes default to
// /waitlist or /login.

import {
  HashRouter,
  clearSession,
  readSession,
} from "@livepeer-network-modules/customer-portal-shared";

const SIGNED_IN_NAV = [
  { href: "#/playground", label: "Playground" },
  { href: "#/prompts", label: "Prompts" },
  { href: "#/usage", label: "Usage" },
  { href: "#/account", label: "Account" },
];

const SIGNED_OUT_NAV = [
  { href: "#/waitlist", label: "Request access" },
  { href: "#/login", label: "Sign in" },
];

export class PortalDaydreamApp extends HTMLElement {
  private router = new HashRouter();
  private root!: HTMLElement;

  connectedCallback(): void {
    this.renderShell();
    this.wireRoutes();
    this.router.start();
    window.addEventListener("hashchange", () => this.renderNav());
    window.addEventListener("storage", () => this.renderNav());
  }

  private renderShell(): void {
    this.innerHTML = `
      <portal-layout>
        <header slot="header" class="portal-daydream-app__header"
          style="display:flex;justify-content:space-between;align-items:center;padding:1rem">
          <strong>Daydream Portal</strong>
          <nav class="portal-daydream-app__nav"></nav>
        </header>
        <main id="portal-daydream-app-root" style="padding:1rem;max-width:960px;margin:0 auto"></main>
      </portal-layout>`;
    this.root = this.querySelector<HTMLElement>("#portal-daydream-app-root")!;
    this.renderNav();
  }

  private renderNav(): void {
    const nav = this.querySelector<HTMLElement>(".portal-daydream-app__nav");
    if (!nav) return;
    const session = readSession();
    const items = session ? SIGNED_IN_NAV : SIGNED_OUT_NAV;
    nav.innerHTML = items
      .map((i) => `<a href="${i.href}" style="margin-left:1rem">${i.label}</a>`)
      .join("") +
      (session
        ? ` <portal-button id="dd-signout" variant="ghost">Sign out</portal-button>`
        : "");
    nav.querySelector("#dd-signout")?.addEventListener("click", () => {
      clearSession();
      location.hash = "#/login";
      this.renderNav();
    });
  }

  private wireRoutes(): void {
    this.router
      .add("/waitlist", () => {
        this.root.innerHTML = "<portal-daydream-waitlist></portal-daydream-waitlist>";
      })
      .add("/login", () => {
        this.root.innerHTML = "<portal-daydream-login></portal-daydream-login>";
      })
      .add("/playground", () => {
        if (!this.requireAuth()) return;
        this.root.innerHTML = "<portal-daydream-playground></portal-daydream-playground>";
      })
      .add("/prompts", () => {
        if (!this.requireAuth()) return;
        this.root.innerHTML = "<portal-daydream-prompts></portal-daydream-prompts>";
      })
      .add("/usage", () => {
        if (!this.requireAuth()) return;
        this.root.innerHTML = "<portal-daydream-usage></portal-daydream-usage>";
      })
      .add("/account", () => {
        if (!this.requireAuth()) return;
        this.root.innerHTML = `
          <portal-card heading="Account">
            <p>Your API key was shown once at issuance. If you've lost
            it, ask your operator to revoke and re-issue.</p>
          </portal-card>`;
      });

    // Default route: send signed-out users to waitlist, signed-in to playground.
    if (!location.hash || location.hash === "#" || location.hash === "#/") {
      location.hash = readSession() ? "#/playground" : "#/waitlist";
    }
  }

  private requireAuth(): boolean {
    if (readSession()) return true;
    location.hash = "#/login";
    return false;
  }
}

customElements.define("portal-daydream-app", PortalDaydreamApp);
