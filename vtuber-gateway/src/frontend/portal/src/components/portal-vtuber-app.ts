import { LitElement, css, html, type TemplateResult } from "lit";
import { customElement, state } from "lit/decorators.js";
import { HashRouter } from "@livepeer-rewrite/customer-portal-shared";

interface RouteState {
  view: string;
  params: Record<string, string>;
}

@customElement("vtuber-gateway-portal")
export class VtuberGatewayPortal extends LitElement {
  @state() private route: RouteState = { view: "sessions", params: {} };

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
      .add("/sessions", () => {
        this.route = { view: "sessions", params: {} };
      })
      .add("/persona", () => {
        this.route = { view: "persona", params: {} };
      })
      .add("/history", () => {
        this.route = { view: "history", params: {} };
      });
    router.start();
  }

  private renderView(): TemplateResult {
    switch (this.route.view) {
      case "sessions":
        return html`<portal-vtuber-sessions></portal-vtuber-sessions>`;
      case "persona":
        return html`<portal-vtuber-persona></portal-vtuber-persona>`;
      case "history":
        return html`<portal-vtuber-history></portal-vtuber-history>`;
      default:
        return html`<portal-vtuber-sessions></portal-vtuber-sessions>`;
    }
  }

  render(): TemplateResult {
    const link = (to: string, label: string, key: string): TemplateResult =>
      html`<a class=${this.route.view === key ? "active" : ""} href="#${to}">${label}</a>`;
    return html`
      <portal-layout brand="Livepeer VTuber">
        <div slot="nav" class="nav">
          ${link("/sessions", "Sessions", "sessions")}
          ${link("/persona", "Persona", "persona")}
          ${link("/history", "History", "history")}
        </div>

        <section class="hero">
          <span class="eyebrow">VTuber Portal</span>
          <h1>Operate persona-driven realtime sessions with the same network language as the rest of Livepeer.</h1>
          <p class="lede">
            This surface is the expressive edge of the control plane: session orchestration,
            persona tuning, and scene continuity, wrapped in the same visual system as the
            OpenAI and video gateways.
          </p>
          <div class="feature-grid">
            <portal-metric-tile label="Mode" value="Session Control"></portal-metric-tile>
            <portal-metric-tile label="Media Shape" value="Realtime VTuber"></portal-metric-tile>
            <portal-metric-tile label="Billing Model" value="Session + Top-up"></portal-metric-tile>
          </div>
        </section>

        <section class="content">
          ${this.renderView()}
        </section>

        <span slot="footer">Livepeer VTuber customer portal</span>
      </portal-layout>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "vtuber-gateway-portal": VtuberGatewayPortal;
  }
}
