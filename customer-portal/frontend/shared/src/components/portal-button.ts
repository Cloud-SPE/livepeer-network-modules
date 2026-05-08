import { LitElement, css, html, type TemplateResult } from 'lit';
import { customElement, property } from 'lit/decorators.js';

export type PortalButtonVariant = 'primary' | 'ghost' | 'danger';

@customElement('portal-button')
export class PortalButton extends LitElement {
  @property({ type: String, reflect: true }) variant: PortalButtonVariant = 'primary';
  @property({ type: Boolean, reflect: true }) block = false;
  @property({ type: String }) type: 'button' | 'submit' | 'reset' = 'button';
  @property({ type: Boolean, reflect: true }) disabled = false;
  @property({ type: Boolean, reflect: true }) loading = false;

  static styles = css`
    :host {
      --_bg: var(--accent);
      --_bg-hover: var(--accent-hover);
      --_fg: white;
      display: inline-flex;
    }
    :host([variant='ghost']) {
      --_bg: transparent;
      --_bg-hover: var(--surface-2);
      --_fg: var(--text-1);
    }
    :host([variant='danger']) {
      --_bg: var(--danger);
      --_bg-hover: var(--danger-hover);
      --_fg: white;
    }
    :host([block]) {
      display: flex;
      width: 100%;
    }
    button {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      gap: var(--space-2);
      padding: 0.72rem 1rem;
      border-radius: var(--radius-pill);
      background:
        linear-gradient(180deg, color-mix(in srgb, var(--_bg), white 8%) 0%, var(--_bg) 100%);
      color: var(--_fg);
      font-weight: 650;
      font-size: var(--font-size-sm);
      font-family: inherit;
      letter-spacing: 0.01em;
      border: 1px solid color-mix(in srgb, var(--_bg), black 18%);
      transition:
        background var(--duration-fast) var(--ease-standard),
        border-color var(--duration-fast) var(--ease-standard),
        box-shadow var(--duration-fast) var(--ease-standard),
        transform var(--duration-fast) var(--ease-standard);
      width: 100%;
      cursor: pointer;
      box-shadow: 0 10px 24px color-mix(in srgb, var(--_bg), transparent 70%);
    }
    button:hover:not(:disabled) {
      background: var(--_bg-hover);
      border-color: color-mix(in srgb, var(--_bg-hover), white 10%);
    }
    button:active:not(:disabled) {
      transform: translateY(1px);
    }
    button:focus-visible {
      outline: 0;
      box-shadow: 0 0 0 3px color-mix(in oklch, var(--_bg), transparent 70%);
    }
    button:disabled {
      opacity: 0.55;
      cursor: not-allowed;
    }
  `;

  render(): TemplateResult {
    return html`
      <button
        type=${this.type}
        ?disabled=${this.disabled || this.loading}
        @click=${this._onClick}
      >
        <slot></slot>
      </button>
    `;
  }

  private _onClick(e: MouseEvent): void {
    if (this.disabled || this.loading) {
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    if (this.type === 'submit') {
      const form = this.closest('form');
      if (form) {
        e.preventDefault();
        form.requestSubmit();
      }
    }
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'portal-button': PortalButton;
  }
}
