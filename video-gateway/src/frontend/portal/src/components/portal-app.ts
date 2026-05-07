import { LitElement, css, html, type TemplateResult } from "lit";
import { customElement, state } from "lit/decorators.js";
import { HashRouter } from "@livepeer-rewrite/customer-portal-shared";

interface RouteState {
  view: string;
  params: Record<string, string>;
}

@customElement("video-gateway-portal")
export class VideoGatewayPortal extends LitElement {
  @state() private route: RouteState = { view: "assets", params: {} };

  static styles = css`
    :host {
      display: block;
      font-family: var(--font-sans, system-ui, sans-serif);
    }
    nav {
      display: flex;
      gap: 0.75rem;
      padding: 0.75rem 1rem;
      border-bottom: 1px solid var(--border-1, #d4d4d8);
      background: var(--surface-1, #fafafa);
      flex-wrap: wrap;
    }
    nav a {
      color: var(--text-1, #18181b);
      text-decoration: none;
      font-weight: 600;
      font-size: 0.875rem;
    }
    nav a.active {
      color: var(--accent-1, #2563eb);
    }
    main {
      padding: 1rem;
    }
  `;

  override connectedCallback(): void {
    super.connectedCallback();
    const router = new HashRouter();
    router
      .add("/signup", () => {
        this.route = { view: "signup", params: {} };
      })
      .add("/login", () => {
        this.route = { view: "login", params: {} };
      })
      .add("/account", () => {
        this.route = { view: "account", params: {} };
      })
      .add("/api-keys", () => {
        this.route = { view: "api-keys", params: {} };
      })
      .add("/billing", () => {
        this.route = { view: "billing", params: {} };
      })
      .add("/assets", () => {
        this.route = { view: "assets", params: {} };
      })
      .add("/streams", () => {
        this.route = { view: "streams", params: {} };
      })
      .add("/webhooks", () => {
        this.route = { view: "webhooks", params: {} };
      })
      .add("/recordings", () => {
        this.route = { view: "recordings", params: {} };
      });
    router.start();
  }

  private renderView(): TemplateResult {
    const { view } = this.route;
    switch (view) {
      case "signup":
        return html`<portal-card heading="Create account"><portal-signup></portal-signup></portal-card>`;
      case "login":
        return html`<portal-card heading="Sign in"><portal-login></portal-login></portal-card>`;
      case "account":
        return html`<portal-card heading="Account"><portal-balance balanceCents="0" reservedCents="0"></portal-balance></portal-card>`;
      case "api-keys":
        return html`<portal-card heading="API keys"><portal-api-keys></portal-api-keys></portal-card>`;
      case "billing":
        return html`<portal-card heading="Billing"><portal-checkout-button amountCents="1000">Top up $10</portal-checkout-button></portal-card>`;
      case "assets":
        return html`<portal-assets></portal-assets>`;
      case "streams":
        return html`<portal-streams></portal-streams>`;
      case "webhooks":
        return html`<portal-webhooks></portal-webhooks>`;
      case "recordings":
        return html`<portal-recordings></portal-recordings>`;
      default:
        return html`<p>not found</p>`;
    }
  }

  render(): TemplateResult {
    const { view } = this.route;
    const link = (to: string, label: string, key: string): TemplateResult =>
      html`<a class=${view === key ? "active" : ""} href="#${to}">${label}</a>`;
    return html`
      <nav>
        ${link("/assets", "Assets", "assets")}
        ${link("/streams", "Streams", "streams")}
        ${link("/recordings", "Recordings", "recordings")}
        ${link("/webhooks", "Webhooks", "webhooks")}
        ${link("/api-keys", "API keys", "api-keys")}
        ${link("/billing", "Billing", "billing")}
        ${link("/account", "Account", "account")}
      </nav>
      <main>${this.renderView()}</main>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "video-gateway-portal": VideoGatewayPortal;
  }
}
