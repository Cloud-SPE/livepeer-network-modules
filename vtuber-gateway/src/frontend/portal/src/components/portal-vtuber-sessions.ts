// Customer "my vtuber sessions" page. Per plan 0013-vtuber OQ3 lock,
// vtuber-specific pages live here (not in customer-portal). Composes
// the shared shell's `<lp-balance-display>` + per-row controls for
// status / topup / end.

import { LitElement, html, css } from "lit";

export class PortalVtuberSessionsPage extends LitElement {
  static override styles = css`
    :host {
      display: grid;
      gap: var(--space-4);
    }
    .intro {
      display: grid;
      gap: var(--space-2);
    }
    .stats {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
      gap: var(--space-3);
    }
    .stat {
      padding: var(--space-4);
      border-radius: var(--radius-lg);
      border: 1px solid var(--border-1);
      background: var(--surface-1);
      box-shadow: var(--shadow-sm);
    }
    .stat-label {
      display: block;
      margin-bottom: var(--space-2);
      color: var(--text-3);
      font-size: var(--font-size-xs);
      text-transform: uppercase;
      letter-spacing: 0.12em;
    }
    .stat-value {
      display: block;
      color: var(--text-1);
      font-size: var(--font-size-lg);
      font-weight: 650;
    }
    .queue {
      display: grid;
      gap: var(--space-3);
    }
    .queue-item {
      padding: var(--space-4);
      border-radius: var(--radius-lg);
      border: 1px solid var(--border-1);
      background:
        linear-gradient(180deg, rgba(255, 255, 255, 0.03) 0%, rgba(255, 255, 255, 0.012) 100%),
        var(--surface-1);
    }
    .queue-title {
      font-weight: 600;
      margin-bottom: var(--space-1);
    }
    .pill {
      display: inline-flex;
      align-items: center;
      min-height: 1.75rem;
      padding: 0.2rem 0.65rem;
      border-radius: var(--radius-pill);
      border: 1px solid var(--accent-line);
      background: var(--accent-tint);
      color: var(--text-1);
      font-size: var(--font-size-xs);
      font-weight: 600;
      letter-spacing: 0.08em;
      text-transform: uppercase;
    }
  `;

  override render() {
    return html`
      <portal-card heading="My VTuber Sessions">
        <div class="intro">
          <span class="pill">Realtime session plane</span>
          <p>
            Active and historical persona sessions land here. This route is where
            operators watch burn-rate, revisit prior scenes, and top up a live
            session before balance pressure ends it.
          </p>
        </div>
      </portal-card>

      <div class="stats">
        <div class="stat">
          <span class="stat-label">Current Surface</span>
          <span class="stat-value">Session Ledger</span>
        </div>
        <div class="stat">
          <span class="stat-label">Pricing Rhythm</span>
          <span class="stat-value">Per-second + top-up</span>
        </div>
        <div class="stat">
          <span class="stat-label">Control Mode</span>
          <span class="stat-value">WS + media plane</span>
        </div>
      </div>

      <portal-card heading="Upcoming UI Work">
        <div class="queue">
          <div class="queue-item">
            <div class="queue-title">Active sessions table</div>
            <p>Session status, current balance runway, top-up action, and end-session control.</p>
          </div>
          <div class="queue-item">
            <div class="queue-title">History timeline</div>
            <p>Recent sessions with persona, scene source, and reconnect / balance events.</p>
          </div>
          <div class="queue-item">
            <div class="queue-title">Control diagnostics</div>
            <p>Surface broker control events like <code>session.balance.low</code> and refill confirmations.</p>
          </div>
        </div>
      </portal-card>
    `;
  }
}

customElements.define("portal-vtuber-sessions", PortalVtuberSessionsPage);
