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
      background: var(--surface-0);
      color: var(--text-1);
      font-family: var(--font-sans);
    }
    header {
      padding: var(--space-3) var(--space-5);
      border-bottom: 1px solid var(--border-1);
      background: var(--surface-1);
    }
    .brand {
      font-weight: 700;
      font-size: var(--font-size-lg);
    }
    main {
      padding: var(--space-5);
    }
    footer {
      padding: var(--space-3) var(--space-5);
      border-top: 1px solid var(--border-1);
      color: var(--text-3);
      font-size: var(--font-size-xs);
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
