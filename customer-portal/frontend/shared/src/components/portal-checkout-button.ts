import { LitElement, html, type TemplateResult } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';

@customElement('portal-checkout-button')
export class PortalCheckoutButton extends LitElement {
  @property({ type: String }) action = '/v1/billing/topup/checkout';
  @property({ type: Number }) amountCents = 1000;
  @property({ type: String }) publishableKey = '';
  @state() private _loading = false;

  render(): TemplateResult {
    return html`
      <portal-button
        ?loading=${this._loading}
        ?disabled=${this._loading}
        @click=${this._onClick}
      >
        <slot>Top up</slot>
      </portal-button>
    `;
  }

  private async _onClick(): Promise<void> {
    this._loading = true;
    try {
      const res = await fetch(this.action, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ amount_usd_cents: this.amountCents }),
      });
      if (!res.ok) {
        throw new Error(`checkout init failed: ${res.status}`);
      }
      const json = (await res.json()) as { url?: string };
      if (!json.url) throw new Error('checkout init returned no url');
      window.location.assign(json.url);
    } catch (err) {
      this.dispatchEvent(
        new CustomEvent('portal-checkout-error', {
          detail: { message: err instanceof Error ? err.message : 'checkout failed' },
          bubbles: true,
          composed: true,
        }),
      );
    } finally {
      this._loading = false;
    }
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'portal-checkout-button': PortalCheckoutButton;
  }
}
