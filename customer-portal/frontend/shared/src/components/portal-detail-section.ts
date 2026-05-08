import { LitElement, css, html, type TemplateResult } from 'lit';
import { customElement, property } from 'lit/decorators.js';

@customElement('portal-detail-section')
export class PortalDetailSection extends LitElement {
  @property({ type: String }) heading = '';
  @property({ type: String }) description = '';

  static styles = css`
    :host {
      display: block;
      margin-top: var(--space-5);
      padding-top: var(--space-4);
      border-top: 1px solid var(--border-1);
    }
    .head {
      display: grid;
      gap: var(--space-1);
      margin-bottom: var(--space-3);
    }
    .heading {
      color: var(--text-1);
      font-size: var(--font-size-base);
      font-weight: 650;
      letter-spacing: -0.01em;
    }
    .description {
      color: var(--text-2);
      font-size: var(--font-size-sm);
    }
  `;

  render(): TemplateResult {
    return html`
      ${this.heading || this.description
        ? html`
            <div class="head">
              ${this.heading ? html`<div class="heading">${this.heading}</div>` : ''}
              ${this.description ? html`<div class="description">${this.description}</div>` : ''}
            </div>
          `
        : ''}
      <slot></slot>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'portal-detail-section': PortalDetailSection;
  }
}
