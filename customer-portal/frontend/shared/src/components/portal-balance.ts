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
      display: flex;
      gap: var(--space-4);
    }
    .stat {
      flex: 1;
    }
    .label {
      font-size: var(--font-size-xs);
      color: var(--text-3);
      text-transform: uppercase;
      letter-spacing: 0.06em;
    }
    .value {
      font-size: var(--font-size-2xl);
      font-weight: 700;
      color: var(--text-1);
      font-variant-numeric: tabular-nums;
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
