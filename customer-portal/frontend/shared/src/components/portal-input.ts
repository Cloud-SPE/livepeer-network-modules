import { LitElement, css, html, type TemplateResult } from 'lit';
import { customElement, property } from 'lit/decorators.js';

@customElement('portal-input')
export class PortalInput extends LitElement {
  @property({ type: String }) name = '';
  @property({ type: String }) label = '';
  @property({ type: String }) type = 'text';
  @property({ type: String }) value = '';
  @property({ type: String }) placeholder = '';
  @property({ type: Boolean }) required = false;
  @property({ type: String }) error = '';

  static styles = css`
    :host {
      display: block;
    }
    label {
      display: block;
      font-size: var(--font-size-sm);
      color: var(--text-2);
      margin-bottom: var(--space-1);
    }
    input {
      width: 100%;
      padding: var(--space-2) var(--space-3);
      border-radius: var(--radius-md);
      border: 1px solid var(--border-1);
      background: var(--surface-0);
      color: var(--text-1);
      font-size: var(--font-size-base);
      font-family: inherit;
    }
    input:focus-visible {
      outline: 0;
      border-color: var(--accent);
      box-shadow: 0 0 0 3px var(--accent-tint);
    }
    .error {
      color: var(--danger);
      font-size: var(--font-size-xs);
      margin-top: var(--space-1);
    }
  `;

  render(): TemplateResult {
    return html`
      ${this.label ? html`<label for=${this.name}>${this.label}</label>` : ''}
      <input
        id=${this.name}
        name=${this.name}
        type=${this.type}
        .value=${this.value}
        placeholder=${this.placeholder}
        ?required=${this.required}
        @input=${this._onInput}
      />
      ${this.error ? html`<div class="error">${this.error}</div>` : ''}
    `;
  }

  private _onInput(e: Event): void {
    const target = e.target as HTMLInputElement;
    this.value = target.value;
    this.dispatchEvent(
      new CustomEvent('portal-input-change', {
        detail: { name: this.name, value: this.value },
        bubbles: true,
        composed: true,
      }),
    );
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'portal-input': PortalInput;
  }
}
