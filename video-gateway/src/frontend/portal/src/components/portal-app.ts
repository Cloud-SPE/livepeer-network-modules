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
    }
    .hero {
      display: grid;
      gap: var(--space-4);
      margin-bottom: var(--space-5);
    }
    .eyebrow {
      color: var(--accent);
      font-size: var(--font-size-xs);
      font-weight: 600;
      letter-spacing: 0.16em;
      text-transform: uppercase;
    }
    h1 {
      font-size: var(--font-size-3xl);
    }
    .lede {
      max-width: 72ch;
    }
    .feature-grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
      gap: var(--space-3);
    }
    .feature {
      padding: var(--space-4);
      border-radius: var(--radius-lg);
      border: 1px solid var(--border-1);
      background:
        linear-gradient(180deg, rgba(255, 255, 255, 0.03) 0%, rgba(255, 255, 255, 0.012) 100%),
        var(--surface-1);
      box-shadow: var(--shadow-sm);
    }
    .feature-label {
      display: block;
      color: var(--text-3);
      font-size: var(--font-size-xs);
      letter-spacing: 0.12em;
      text-transform: uppercase;
      margin-bottom: var(--space-2);
    }
    .feature-value {
      display: block;
      font-size: var(--font-size-lg);
      font-weight: 650;
      color: var(--text-1);
      letter-spacing: -0.02em;
    }
    .nav {
      display: flex;
      flex-wrap: wrap;
      gap: var(--space-2);
    }
    .nav a {
      display: inline-flex;
      align-items: center;
      min-height: 2.5rem;
      padding: 0.55rem 0.95rem;
      border-radius: var(--radius-pill);
      border: 1px solid var(--border-1);
      background: rgba(255, 255, 255, 0.02);
      color: var(--text-2);
      text-decoration: none;
      font-weight: 600;
      font-size: var(--font-size-sm);
      transition:
        background var(--duration-fast) var(--ease-standard),
        border-color var(--duration-fast) var(--ease-standard),
        color var(--duration-fast) var(--ease-standard);
    }
    .nav a:hover {
      background: rgba(255, 255, 255, 0.04);
      color: var(--text-1);
    }
    .nav a.active {
      color: var(--text-1);
      border-color: var(--accent-line);
      background: var(--accent-tint);
    }
    .content {
      display: grid;
      gap: var(--space-4);
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
      <portal-layout brand="Livepeer Video">
        <div slot="nav" class="nav">
          ${link("/assets", "Assets", "assets")}
          ${link("/streams", "Streams", "streams")}
          ${link("/recordings", "Recordings", "recordings")}
          ${link("/webhooks", "Webhooks", "webhooks")}
          ${link("/api-keys", "API keys", "api-keys")}
          ${link("/billing", "Billing", "billing")}
          ${link("/account", "Account", "account")}
        </div>

        <section class="hero">
          <span class="eyebrow">Video Portal</span>
          <h1>Run livestreams, asset delivery, and webhook automation from one account.</h1>
          <p class="lede">
            The product surface mirrors the Livepeer network brand: premium,
            technical, and dense where it needs to be. Each route below inherits
            the same tokens and component language as the operator surfaces.
          </p>
          <div class="feature-grid">
            <portal-metric-tile label="Routing" value="Manifest-selected"></portal-metric-tile>
            <portal-metric-tile label="Core Workloads" value="Streams + Assets"></portal-metric-tile>
            <portal-metric-tile label="Account Surface" value="Portal + Billing"></portal-metric-tile>
          </div>
        </section>

        <section class="content">
          ${this.renderView()}
        </section>

        <span slot="footer">Livepeer video customer portal</span>
      </portal-layout>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "video-gateway-portal": VideoGatewayPortal;
  }
}
