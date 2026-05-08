// Persona authoring page — system-prompt + tone preset. Phase 4
// scaffold; full implementation lands in a follow-up commit.

import { LitElement, html, css } from "lit";

export class PortalVtuberPersonaPage extends LitElement {
  static override styles = css`
    :host {
      display: grid;
      gap: var(--space-4);
    }
    .grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(16rem, 1fr));
      gap: var(--space-3);
    }
    .panel {
      padding: var(--space-4);
      border-radius: var(--radius-lg);
      border: 1px solid var(--border-1);
      background: var(--surface-1);
      box-shadow: var(--shadow-sm);
    }
    .eyebrow {
      display: block;
      margin-bottom: var(--space-2);
      color: var(--text-3);
      font-size: var(--font-size-xs);
      letter-spacing: 0.12em;
      text-transform: uppercase;
    }
    h3 {
      margin-bottom: var(--space-2);
    }
  `;

  override render() {
    return html`
      <portal-card heading="Persona Authoring">
        <p>
          Shape the speaking style, system prompt, and scene behavior used when a
          session opens. This route should feel like a premium control surface,
          not a generic form dump.
        </p>
      </portal-card>

      <div class="grid">
        <section class="panel">
          <span class="eyebrow">Prompt Core</span>
          <h3>Voice definition</h3>
          <p>
            Long-form persona description, guardrails, and conversational tone presets.
          </p>
        </section>
        <section class="panel">
          <span class="eyebrow">Realtime Behavior</span>
          <h3>Session defaults</h3>
          <p>
            Expression cadence, emotional range, and fallback behavior when model
            latency or transport quality degrades.
          </p>
        </section>
        <section class="panel">
          <span class="eyebrow">Brand Consistency</span>
          <h3>Reusable presets</h3>
          <p>
            Saved persona kits for recurring shows, characters, or customer-facing demos.
          </p>
        </section>
      </div>
    `;
  }
}

customElements.define("portal-vtuber-persona", PortalVtuberPersonaPage);
