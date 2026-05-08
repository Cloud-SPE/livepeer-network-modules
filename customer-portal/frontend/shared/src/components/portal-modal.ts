import { LitElement, css, html, type TemplateResult } from 'lit';
import { customElement, property } from 'lit/decorators.js';

@customElement('portal-modal')
export class PortalModal extends LitElement {
  @property({ type: Boolean, reflect: true }) open = false;
  @property({ type: String }) heading = '';

  static styles = css`
    :host {
      display: none;
      position: fixed;
      inset: 0;
      background: color-mix(in srgb, black, transparent 42%);
      align-items: center;
      justify-content: center;
      z-index: 100;
      backdrop-filter: blur(12px);
    }
    :host([open]) {
      display: flex;
    }
    .panel {
      background:
        linear-gradient(180deg, rgba(255, 255, 255, 0.045) 0%, rgba(255, 255, 255, 0.02) 100%),
        var(--surface-1);
      border-radius: var(--radius-lg);
      padding: var(--space-5);
      max-width: 32rem;
      width: 90vw;
      box-shadow: var(--shadow-lg);
      border: 1px solid var(--border-1);
    }
    .heading {
      font-size: var(--font-size-lg);
      font-weight: 650;
      margin-bottom: var(--space-3);
      letter-spacing: -0.02em;
    }
  `;

  render(): TemplateResult {
    return html`
      <div class="panel">
        ${this.heading ? html`<div class="heading">${this.heading}</div>` : ''}
        <slot></slot>
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'portal-modal': PortalModal;
  }
}
