import { LitElement, css, html, type TemplateResult } from "lit";
import { customElement, state } from "lit/decorators.js";
import { HashRouter } from "@livepeer-rewrite/customer-portal-shared";

export const VIDEO_GATEWAY_ADMIN_APP_TAG = "video-gateway-admin";

interface RouteState {
  view: string;
  params: Record<string, string>;
}

@customElement("video-gateway-admin")
export class VideoGatewayAdmin extends LitElement {
  @state() private route: RouteState = { view: "customers", params: {} };

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
      font: 600 var(--font-size-xs) / 1 var(--font-sans);
      letter-spacing: 0.16em;
      text-transform: uppercase;
    }
    h1 {
      font-size: var(--font-size-3xl);
    }
    .lede {
      max-width: 72ch;
      color: var(--text-2);
    }
    .metrics {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
      gap: var(--space-3);
    }
    .metric {
      padding: var(--space-4);
      border: 1px solid var(--border-1);
      border-radius: var(--radius-lg);
      background:
        linear-gradient(180deg, rgba(255, 255, 255, 0.035) 0%, rgba(255, 255, 255, 0.015) 100%),
        var(--surface-1);
      box-shadow: var(--shadow-sm);
    }
    .metric-label {
      display: block;
      margin-bottom: var(--space-2);
      color: var(--text-3);
      font-size: var(--font-size-xs);
      text-transform: uppercase;
      letter-spacing: 0.12em;
    }
    .metric-value {
      display: block;
      color: var(--text-1);
      font-size: var(--font-size-xl);
      font-weight: 650;
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
      box-shadow: inset 0 0 0 1px rgba(24, 121, 78, 0.16);
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
      .add("/customers", () => {
        this.route = { view: "customers", params: {} };
      })
      .add("/customers/:id", (_p, params) => {
        this.route = { view: "customer-detail", params };
      })
      .add("/customers/:id/adjust", (_p, params) => {
        this.route = { view: "customer-adjust", params };
      })
      .add("/customers/:id/refund", (_p, params) => {
        this.route = { view: "customer-refund", params };
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
    const { view, params } = this.route;
    switch (view) {
      case "customers":
        return html`<admin-customers></admin-customers>`;
      case "customer-detail":
        return html`<admin-customer-detail customerId=${params["id"] ?? ""}></admin-customer-detail>`;
      case "customer-adjust":
        return html`<admin-customer-adjust customerId=${params["id"] ?? ""}></admin-customer-adjust>`;
      case "customer-refund":
        return html`<admin-customer-refund customerId=${params["id"] ?? ""}></admin-customer-refund>`;
      case "assets":
        return html`<admin-assets></admin-assets>`;
      case "streams":
        return html`<admin-streams></admin-streams>`;
      case "webhooks":
        return html`<admin-webhooks></admin-webhooks>`;
      case "recordings":
        return html`<admin-recordings></admin-recordings>`;
      default:
        return html`<p>not found</p>`;
    }
  }

  render(): TemplateResult {
    const { view } = this.route;
    const link = (to: string, label: string, key: string): TemplateResult =>
      html`<a class=${view === key ? "active" : ""} href="#${to}">${label}</a>`;
    return html`
      <portal-layout brand="Livepeer Network Console">
        <div slot="nav" class="nav">
          ${link("/customers", "Customers", "customers")}
          ${link("/assets", "Assets", "assets")}
          ${link("/streams", "Streams", "streams")}
          ${link("/webhooks", "Webhooks", "webhooks")}
          ${link("/recordings", "Recordings", "recordings")}
        </div>

        <section class="hero">
          <span class="eyebrow">Video Gateway</span>
          <h1>Operator surface for streams, assets, and webhook delivery.</h1>
          <p class="lede">
            This console keeps customer lifecycle, live ingest, VOD capture, and
            downstream delivery in one place. It uses the same Livepeer network
            language as the rest of the control plane.
          </p>
          <div class="metrics">
            <portal-metric-tile label="Control Plane" value="Network Console"></portal-metric-tile>
            <portal-metric-tile label="Routing Model" value="Manifest-driven"></portal-metric-tile>
            <portal-metric-tile label="Primary Domain" value="Video + VOD"></portal-metric-tile>
          </div>
        </section>

        <section class="content">
          ${this.renderView()}
        </section>

        <span slot="footer">Livepeer video gateway operator console</span>
      </portal-layout>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "video-gateway-admin": VideoGatewayAdmin;
  }
}
