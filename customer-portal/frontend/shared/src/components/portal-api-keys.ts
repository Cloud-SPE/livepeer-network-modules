import { LitElement, css, html, type TemplateResult } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';

export interface ApiKeySummary {
  id: string;
  label: string | null;
  createdAt: string;
  lastUsedAt: string | null;
  revokedAt: string | null;
}

@customElement('portal-api-keys')
export class PortalApiKeys extends LitElement {
  @property({ type: Array }) keys: readonly ApiKeySummary[] = [];
  @state() private _newPlaintext: string | null = null;
  @state() private _newLabel = '';

  static styles = css`
    :host {
      display: block;
    }
    table {
      width: 100%;
      border-collapse: collapse;
    }
    th,
    td {
      text-align: left;
      padding: var(--space-2) var(--space-3);
      border-bottom: 1px solid var(--border-1);
      font-size: var(--font-size-sm);
    }
    .plaintext {
      font-family: var(--font-mono);
      background: var(--surface-2);
      padding: var(--space-3);
      border-radius: var(--radius-md);
      word-break: break-all;
      margin-bottom: var(--space-3);
    }
    .form {
      display: flex;
      gap: var(--space-2);
      margin-bottom: var(--space-4);
    }
  `;

  render(): TemplateResult {
    return html`
      ${this._newPlaintext
        ? html`<div class="plaintext">
            <strong>Save this key — it will not be shown again:</strong>
            <div>${this._newPlaintext}</div>
          </div>`
        : ''}
      <div class="form">
        <portal-input
          name="label"
          label=""
          placeholder="Key label"
          .value=${this._newLabel}
          @portal-input-change=${(e: CustomEvent<{ value: string }>) =>
            (this._newLabel = e.detail.value)}
        ></portal-input>
        <portal-button @click=${this._onIssue}>Issue key</portal-button>
      </div>
      <table>
        <thead>
          <tr>
            <th>Label</th>
            <th>Created</th>
            <th>Last used</th>
            <th>Status</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          ${this.keys.map(
            (k) => html`
              <tr>
                <td>${k.label ?? '(unlabeled)'}</td>
                <td>${k.createdAt}</td>
                <td>${k.lastUsedAt ?? '—'}</td>
                <td>${k.revokedAt ? 'Revoked' : 'Active'}</td>
                <td>
                  ${k.revokedAt
                    ? ''
                    : html`<portal-button
                        variant="danger"
                        @click=${() => this._onRevoke(k.id)}
                        >Revoke</portal-button
                      >`}
                </td>
              </tr>
            `,
          )}
        </tbody>
      </table>
    `;
  }

  private _onIssue(): void {
    this.dispatchEvent(
      new CustomEvent('portal-api-key-issue', {
        detail: { label: this._newLabel },
        bubbles: true,
        composed: true,
      }),
    );
  }

  private _onRevoke(id: string): void {
    this.dispatchEvent(
      new CustomEvent('portal-api-key-revoke', {
        detail: { id },
        bubbles: true,
        composed: true,
      }),
    );
  }

  showPlaintext(plaintext: string): void {
    this._newPlaintext = plaintext;
    this._newLabel = '';
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'portal-api-keys': PortalApiKeys;
  }
}
