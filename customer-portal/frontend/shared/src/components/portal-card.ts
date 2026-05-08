import { LitElement, css, html, type TemplateResult } from 'lit';
import { customElement, property } from 'lit/decorators.js';

@customElement('portal-card')
export class PortalCard extends LitElement {
  @property({ type: String }) heading = '';

  static styles = css`
    :host {
      display: block;
      background:
        linear-gradient(180deg, rgba(255, 255, 255, 0.035) 0%, rgba(255, 255, 255, 0.02) 100%),
        var(--surface-1);
      border: 1px solid var(--border-1);
      border-radius: var(--radius-lg);
      padding: var(--space-5);
      box-shadow: var(--shadow-sm);
      backdrop-filter: blur(14px);
    }
    .heading {
      font-size: var(--font-size-lg);
      font-weight: 650;
      color: var(--text-1);
      margin-bottom: var(--space-3);
      letter-spacing: -0.02em;
    }
  `;

  render(): TemplateResult {
    return html`
      ${this.heading ? html`<div class="heading">${this.heading}</div>` : ''}
      <slot></slot>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'portal-card': PortalCard;
  }
}
