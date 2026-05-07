// Persona authoring page — system-prompt + tone preset. Phase 4
// scaffold; full implementation lands in a follow-up commit.

import { LitElement, html, css } from "lit";

export class PortalVtuberPersonaPage extends LitElement {
  static override styles = css`
    :host {
      display: block;
      padding: 1rem;
      font-family: system-ui, sans-serif;
    }
  `;

  override render() {
    return html`
      <h1>Persona Authoring</h1>
      <p>
        System-prompt + tone preset for new sessions. Phase 4 scaffold.
      </p>
    `;
  }
}

customElements.define("portal-vtuber-persona", PortalVtuberPersonaPage);
