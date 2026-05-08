import { LitElement, css, html, type TemplateResult } from 'lit';
import { customElement, property } from 'lit/decorators.js';

export type PortalStatusPillVariant =
  | 'neutral'
  | 'info'
  | 'success'
  | 'warning'
  | 'danger';

@customElement('portal-status-pill')
export class PortalStatusPill extends LitElement {
  @property({ type: String, reflect: true }) variant: PortalStatusPillVariant = 'neutral';

  static styles = css`
    :host {
      --_bg: rgba(255, 255, 255, 0.05);
      --_border: var(--border-1);
      --_fg: var(--text-2);
      display: inline-flex;
      align-items: center;
      min-height: 1.75rem;
      padding: 0.2rem 0.65rem;
      border-radius: var(--radius-pill);
      border: 1px solid var(--_border);
      background: var(--_bg);
      color: var(--_fg);
      font-size: var(--font-size-xs);
      font-weight: 650;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      white-space: nowrap;
    }
    :host([variant='info']) {
      --_bg: var(--accent-tint);
      --_border: var(--accent-line);
      --_fg: var(--text-1);
    }
    :host([variant='success']) {
      --_bg: var(--success-tint);
      --_border: color-mix(in srgb, var(--success), white 12%);
      --_fg: var(--text-1);
    }
    :host([variant='warning']) {
      --_bg: var(--warning-tint);
      --_border: color-mix(in srgb, var(--warning), white 8%);
      --_fg: var(--text-1);
    }
    :host([variant='danger']) {
      --_bg: var(--danger-tint);
      --_border: color-mix(in srgb, var(--danger), white 10%);
      --_fg: var(--text-1);
    }
  `;

  render(): TemplateResult {
    return html`<slot></slot>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'portal-status-pill': PortalStatusPill;
  }
}
