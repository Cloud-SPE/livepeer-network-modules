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
      font-family: var(--font-sans, system-ui, sans-serif);
    }
    nav {
      display: flex;
      gap: 0.75rem;
      padding: 0.75rem 1rem;
      border-bottom: 1px solid var(--border-1, #d4d4d8);
      background: var(--surface-1, #fafafa);
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
      <nav>
        ${link("/customers", "Customers", "customers")}
        ${link("/assets", "Assets", "assets")}
        ${link("/streams", "Streams", "streams")}
        ${link("/webhooks", "Webhooks", "webhooks")}
        ${link("/recordings", "Recordings", "recordings")}
      </nav>
      <main>${this.renderView()}</main>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "video-gateway-admin": VideoGatewayAdmin;
  }
}
