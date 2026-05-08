import { LitElement, css, html, type TemplateResult } from 'lit';
import { customElement, property } from 'lit/decorators.js';

@customElement('portal-action-row')
export class PortalActionRow extends LitElement {
  @property({ type: String, reflect: true }) align: 'start' | 'end' = 'start';

  static styles = css`
    :host {
      display: flex;
      justify-content: flex-start;
    }
    :host([align='end']) {
      justify-content: flex-end;
    }
    .row {
      display: inline-flex;
      flex-wrap: wrap;
      align-items: center;
      gap: var(--space-2);
    }
    ::slotted(portal-button),
    ::slotted(button) {
      flex: 0 0 auto;
    }
  `;

  render(): TemplateResult {
    return html`<div class="row"><slot></slot></div>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'portal-action-row': PortalActionRow;
  }
}
