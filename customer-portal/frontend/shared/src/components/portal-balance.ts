import { LitElement, css, html, type TemplateResult } from 'lit';
import { customElement, property } from 'lit/decorators.js';

@customElement('portal-balance')
export class PortalBalance extends LitElement {
  @property({ type: String }) currency = 'USD';
  @property({ type: Number }) balanceCents = 0;
  @property({ type: Number }) reservedCents = 0;

  static styles = css`
    :host {
      display: block;
    }
    .row {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
      gap: var(--space-3);
    }
    .stat {
      padding: var(--space-4);
      border-radius: var(--radius-lg);
      border: 1px solid var(--border-1);
      background:
        linear-gradient(180deg, rgba(255, 255, 255, 0.03) 0%, rgba(255, 255, 255, 0.012) 100%),
        var(--surface-1);
      box-shadow: var(--shadow-sm);
    }
    .label {
      font-size: var(--font-size-xs);
      color: var(--text-3);
      text-transform: uppercase;
      letter-spacing: 0.06em;
    }
    .value {
      font-size: var(--font-size-2xl);
      font-weight: 650;
      color: var(--text-1);
      font-variant-numeric: tabular-nums;
      letter-spacing: -0.02em;
    }
    .reserved {
      color: var(--text-2);
      font-size: var(--font-size-base);
    }
  `;

  private format(cents: number): string {
    const dollars = cents / 100;
    return dollars.toLocaleString(undefined, {
      style: 'currency',
      currency: this.currency,
    });
  }

  render(): TemplateResult {
    return html`
      <div class="row">
        <div class="stat">
          <div class="label">Available</div>
          <div class="value">${this.format(this.balanceCents - this.reservedCents)}</div>
        </div>
        <div class="stat">
          <div class="label">Reserved</div>
          <div class="value reserved">${this.format(this.reservedCents)}</div>
        </div>
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'portal-balance': PortalBalance;
  }
}
