// Customer "my vtuber sessions" page. Per plan 0013-vtuber OQ3 lock,
// vtuber-specific pages live here (not in customer-portal). Composes
// the shared shell's `<lp-balance-display>` + per-row controls for
// status / topup / end.

import { LitElement, html, css } from "lit";

export class PortalVtuberSessionsPage extends LitElement {
  static override styles = css`
    :host {
      display: block;
      padding: 1rem;
      font-family: system-ui, sans-serif;
    }
    h1 {
      margin-top: 0;
    }
  `;

  override render() {
    return html`
      <h1>My Vtuber Sessions</h1>
      <p>
        Live + historical sessions. Per-second metering reflected in
        the balance widget above. Phase 4 scaffold.
      </p>
    `;
  }
}

customElements.define("portal-vtuber-sessions", PortalVtuberSessionsPage);
