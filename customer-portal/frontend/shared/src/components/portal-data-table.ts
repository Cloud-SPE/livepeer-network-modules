import { LitElement, css, html, type TemplateResult } from 'lit';
import { customElement, property } from 'lit/decorators.js';

@customElement('portal-data-table')
export class PortalDataTable extends LitElement {
  @property({ type: String }) heading = '';
  @property({ type: String }) description = '';

  static styles = css`
    :host {
      display: block;
    }
    .shell {
      display: grid;
      gap: var(--space-4);
    }
    .head {
      display: grid;
      gap: var(--space-2);
    }
    .heading {
      color: var(--text-1);
      font-size: var(--font-size-lg);
      font-weight: 650;
      letter-spacing: -0.02em;
    }
    .description {
      color: var(--text-2);
      font-size: var(--font-size-sm);
    }
    .toolbar {
      display: flex;
      flex-wrap: wrap;
      gap: var(--space-2);
      align-items: center;
      justify-content: space-between;
    }
    .frame {
      overflow: hidden;
      border: 1px solid var(--border-1);
      border-radius: var(--radius-lg);
      background: rgba(255, 255, 255, 0.02);
      box-shadow: var(--shadow-sm);
    }
    ::slotted(table) {
      width: 100%;
      border-collapse: collapse;
      font-size: var(--font-size-sm);
    }
  `;

  render(): TemplateResult {
    return html`
      <div class="shell">
        ${this.heading || this.description
          ? html`
              <div class="head">
                ${this.heading ? html`<div class="heading">${this.heading}</div>` : ''}
                ${this.description ? html`<div class="description">${this.description}</div>` : ''}
              </div>
            `
          : ''}
        <div class="toolbar">
          <slot name="toolbar"></slot>
        </div>
        <div class="frame">
          <slot></slot>
        </div>
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'portal-data-table': PortalDataTable;
  }
}
