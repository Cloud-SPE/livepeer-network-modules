import { LitElement, css, html, type TemplateResult } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';

@customElement('portal-login')
export class PortalLogin extends LitElement {
  @property({ type: String }) action = '/v1/account/login';
  @state() private _error = '';

  static styles = css`
    :host {
      display: block;
    }
    form {
      display: grid;
      gap: var(--space-3);
    }
  `;

  render(): TemplateResult {
    return html`
      <form @submit=${this._onSubmit}>
        <portal-input name="email" type="email" label="Email" required></portal-input>
        <portal-input name="password" type="password" label="Password" required></portal-input>
        <portal-button type="submit" block>Sign in</portal-button>
        ${this._error ? html`<portal-toast variant="danger" message=${this._error}></portal-toast>` : ''}
      </form>
    `;
  }

  private async _onSubmit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    const form = e.target as HTMLFormElement;
    const data = new FormData(form);
    try {
      const res = await fetch(this.action, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          email: data.get('email'),
          password: data.get('password'),
        }),
      });
      if (!res.ok) {
        this._error = `login failed (${res.status})`;
        return;
      }
      this.dispatchEvent(
        new CustomEvent('portal-login-success', { bubbles: true, composed: true }),
      );
    } catch (err) {
      this._error = err instanceof Error ? err.message : 'login failed';
    }
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'portal-login': PortalLogin;
  }
}
