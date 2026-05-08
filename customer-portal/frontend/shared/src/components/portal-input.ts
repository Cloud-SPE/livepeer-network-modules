import { LitElement, css, html, type TemplateResult } from 'lit';
import { customElement, property } from 'lit/decorators.js';

@customElement('portal-input')
export class PortalInput extends LitElement {
  static readonly formAssociated = true;

  @property({ type: String }) name = '';
  @property({ type: String }) label = '';
  @property({ type: String }) type = 'text';
  @property({ type: String }) value = '';
  @property({ type: String }) placeholder = '';
  @property({ type: Boolean }) required = false;
  @property({ type: String }) error = '';

  private readonly _internals =
    typeof this.attachInternals === 'function' ? this.attachInternals() : null;

  static styles = css`
    :host {
      display: block;
    }
    label {
      display: block;
      font-size: var(--font-size-sm);
      color: var(--text-2);
      margin-bottom: var(--space-1);
      font-weight: 550;
    }
    input {
      width: 100%;
      min-height: 2.8rem;
      padding: var(--space-2) var(--space-3);
      border-radius: var(--radius-md);
      border: 1px solid var(--border-1);
      background: rgba(255, 255, 255, 0.03);
      color: var(--text-1);
      font-size: var(--font-size-base);
      font-family: inherit;
      transition:
        border-color var(--duration-fast) var(--ease-standard),
        box-shadow var(--duration-fast) var(--ease-standard),
        background var(--duration-fast) var(--ease-standard);
    }
    input:focus-visible {
      outline: 0;
      border-color: var(--accent-line);
      box-shadow: 0 0 0 3px var(--accent-tint);
      background: rgba(255, 255, 255, 0.045);
    }
    .error {
      color: var(--danger);
      font-size: var(--font-size-xs);
      margin-top: var(--space-1);
    }
  `;

  override connectedCallback(): void {
    super.connectedCallback();
    this._syncFormState();
  }

  override updated(): void {
    this._syncFormState();
  }

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

  private _syncFormState(): void {
    if (!this._internals) return;
    this._internals.setFormValue(this.value);
    if (this.required && !this.value) {
      this._internals.setValidity({ valueMissing: true }, 'Please fill out this field.');
      return;
    }
    this._internals.setValidity({});
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'portal-input': PortalInput;
  }
}
