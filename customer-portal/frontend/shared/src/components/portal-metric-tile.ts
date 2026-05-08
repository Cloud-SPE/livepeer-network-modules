import { LitElement, css, html, type TemplateResult } from 'lit';
import { customElement, property } from 'lit/decorators.js';

@customElement('portal-metric-tile')
export class PortalMetricTile extends LitElement {
  @property({ type: String }) label = '';
  @property({ type: String }) value = '';
  @property({ type: String }) detail = '';

  static styles = css`
    :host {
      display: block;
      padding: var(--space-4);
      border-radius: var(--radius-lg);
      border: 1px solid var(--border-1);
      background:
        linear-gradient(180deg, rgba(255, 255, 255, 0.035) 0%, rgba(255, 255, 255, 0.015) 100%),
        var(--surface-1);
      box-shadow: var(--shadow-sm);
    }
    .label {
      display: block;
      margin-bottom: var(--space-2);
      color: var(--text-3);
      font-size: var(--font-size-xs);
      text-transform: uppercase;
      letter-spacing: 0.12em;
    }
    .value {
      display: block;
      color: var(--text-1);
      font-size: var(--font-size-xl);
      font-weight: 650;
      letter-spacing: -0.02em;
    }
    .detail {
      display: block;
      margin-top: var(--space-2);
      color: var(--text-2);
      font-size: var(--font-size-sm);
    }
  `;

  render(): TemplateResult {
    return html`
      ${this.label ? html`<span class="label">${this.label}</span>` : ''}
      ${this.value ? html`<span class="value">${this.value}</span>` : html`<span class="value"><slot name="value"></slot></span>`}
      ${this.detail ? html`<span class="detail">${this.detail}</span>` : html`<slot name="detail"></slot>`}
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'portal-metric-tile': PortalMetricTile;
  }
}
