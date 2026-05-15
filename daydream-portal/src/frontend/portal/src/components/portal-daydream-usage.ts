// Personal usage view. Two slices: the 30-day rollup tile up top and a
// recent-sessions table below. No credits, no quota, no $: this is
// purely "what did I run." Operator-wide rollup lives in the admin SPA.

import {
  DaydreamPortalApi,
  type UsageEvent,
  type UsageSummary,
} from "../lib/api.js";

export class PortalDaydreamUsage extends HTMLElement {
  private api = new DaydreamPortalApi();
  private summary: UsageSummary | null = null;
  private events: UsageEvent[] = [];
  private loading = true;
  private error: string | null = null;

  connectedCallback(): void {
    this.render();
    void this.load();
  }

  private async load(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const [summary, recent] = await Promise.all([
        this.api.usageSummary(),
        this.api.usageRecent(),
      ]);
      this.summary = summary;
      this.events = recent.events;
    } catch (err) {
      this.error = `Could not load usage: ${err instanceof Error ? err.message : String(err)}`;
    } finally {
      this.loading = false;
      this.render();
    }
  }

  private render(): void {
    this.innerHTML = `
      <portal-card heading="Usage">
        ${this.loading ? "<p>Loading…</p>" : ""}
        ${this.error ? `<p class="portal-form-error">${escapeHtml(this.error)}</p>` : ""}
        ${
          this.summary
            ? `<div class="portal-daydream-usage__tiles" style="display:flex;gap:1rem;flex-wrap:wrap">
                 <portal-metric-tile label="Sessions (30d)" value="${this.summary.total_sessions}"></portal-metric-tile>
                 <portal-metric-tile label="Total seconds" value="${this.summary.total_seconds}"></portal-metric-tile>
                 <portal-metric-tile label="Total minutes" value="${Math.round(this.summary.total_seconds / 60)}"></portal-metric-tile>
               </div>`
            : ""
        }
        ${
          this.events.length > 0
            ? `<h3 style="margin-top:1.5rem">Recent sessions</h3>
               <table class="portal-daydream-usage__table" style="width:100%;border-collapse:collapse;font-size:.9em">
                 <thead>
                   <tr>
                     <th style="text-align:left;padding:.25rem">Session</th>
                     <th style="text-align:left;padding:.25rem">Orchestrator</th>
                     <th style="text-align:left;padding:.25rem">Started</th>
                     <th style="text-align:right;padding:.25rem">Duration (s)</th>
                   </tr>
                 </thead>
                 <tbody>
                   ${this.events.map((e) => this.renderRow(e)).join("")}
                 </tbody>
               </table>`
            : !this.loading && !this.error
              ? "<p>No sessions yet.</p>"
              : ""
        }
      </portal-card>`;
  }

  private renderRow(e: UsageEvent): string {
    return `
      <tr style="border-top:1px solid var(--portal-border,#eee)">
        <td style="padding:.25rem"><code>${escapeHtml(e.session_id.slice(0, 12))}…</code></td>
        <td style="padding:.25rem"><code>${escapeHtml(e.orchestrator ?? "—")}</code></td>
        <td style="padding:.25rem">${escapeHtml(new Date(e.started_at).toLocaleString())}</td>
        <td style="padding:.25rem;text-align:right">${e.duration_seconds ?? "—"}</td>
      </tr>`;
  }
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

customElements.define("portal-daydream-usage", PortalDaydreamUsage);
