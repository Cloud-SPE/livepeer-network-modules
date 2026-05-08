import { LitElement, css, html, type TemplateResult } from 'lit';
import { customElement, property } from 'lit/decorators.js';

@customElement('portal-layout')
export class PortalLayout extends LitElement {
  @property({ type: String }) brand = 'Customer Portal';

  static styles = css`
    :host {
      display: grid;
      grid-template-rows: auto 1fr auto;
      min-height: 100vh;
      background:
        radial-gradient(circle at top left, var(--accent-tint), transparent 22rem),
        linear-gradient(180deg, var(--surface-canvas) 0%, var(--surface-0) 100%);
      color: var(--text-1);
      font-family: var(--font-sans);
    }
    header {
      padding: var(--space-3) var(--space-5);
      border-bottom: 1px solid var(--border-1);
      background: var(--surface-overlay);
      backdrop-filter: blur(18px);
    }
    .brand {
      font-weight: 700;
      font-size: var(--font-size-lg);
      letter-spacing: -0.02em;
    }
    main {
      padding: var(--space-6) var(--space-5);
      width: min(1120px, 100%);
      margin: 0 auto;
    }
    footer {
      padding: var(--space-3) var(--space-5);
      border-top: 1px solid var(--border-1);
      color: var(--text-3);
      font-size: var(--font-size-xs);
      background: rgba(255, 255, 255, 0.02);
    }
  `;

  render(): TemplateResult {
    return html`
      <header>
        <div class="brand">${this.brand}</div>
        <slot name="nav"></slot>
      </header>
      <main>
        <slot></slot>
      </main>
      <footer>
        <slot name="footer"></slot>
      </footer>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'portal-layout': PortalLayout;
  }
}
