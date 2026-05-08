import { LitElement, css, html, type TemplateResult } from 'lit';
import { customElement, property } from 'lit/decorators.js';

export type ToastVariant = 'info' | 'success' | 'warning' | 'danger';

@customElement('portal-toast')
export class PortalToast extends LitElement {
  @property({ type: String, reflect: true }) variant: ToastVariant = 'info';
  @property({ type: String }) message = '';

  static styles = css`
    :host {
      display: block;
      padding: var(--space-3) var(--space-4);
      border-radius: var(--radius-md);
      background:
        linear-gradient(180deg, rgba(255, 255, 255, 0.03) 0%, rgba(255, 255, 255, 0.015) 100%),
        var(--surface-2);
      color: var(--text-1);
      border: 1px solid var(--border-1);
      font-size: var(--font-size-sm);
      box-shadow: var(--shadow-sm);
    }
    :host([variant='success']) {
      background: var(--success-tint);
      border-color: var(--success);
    }
    :host([variant='warning']) {
      background: var(--warning-tint);
      border-color: var(--warning);
    }
    :host([variant='danger']) {
      background: var(--danger-tint);
      border-color: var(--danger);
    }
  `;

  render(): TemplateResult {
    return html`<span>${this.message}</span><slot></slot>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'portal-toast': PortalToast;
  }
}
