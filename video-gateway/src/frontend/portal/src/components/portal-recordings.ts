import { LitElement, css, html, type TemplateResult } from "lit";
import { customElement, state } from "lit/decorators.js";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";

interface StreamRow {
  id: string;
  name: string;
  recordToVod: boolean;
}

interface RecordingRow {
  id: string;
  streamId: string;
  streamName: string;
  assetId: string | null;
  status: string;
  durationSec: number | null;
  startedAt: string;
  endedAt: string | null;
}

@customElement("portal-recordings")
export class PortalRecordings extends LitElement {
  @state() private streams: StreamRow[] = [];
  @state() private recordings: RecordingRow[] = [];
  @state() private error: string | null = null;
  @state() private busy: Record<string, boolean> = {};

  private api = new ApiClient({ baseUrl: "" });

  static styles = css`
    :host { display: block; }
    section { margin-top: var(--space-5); }
    table { width: 100%; border-collapse: collapse; font-size: var(--font-size-sm); }
    th, td { padding: 0.7rem 0.75rem; border-bottom: 1px solid var(--border-1); text-align: left; }
    th {
      color: var(--text-3);
      font-size: var(--font-size-xs);
      font-weight: 650;
      text-transform: uppercase;
      letter-spacing: 0.1em;
    }
    tbody tr:hover { background: rgba(255, 255, 255, 0.02); }
    a { color: var(--accent); }
    .err { color: var(--danger); }
    .note { color: var(--text-3); font-size: 0.75rem; }
    .dim { color: var(--text-3); }
  `;

  override async connectedCallback(): Promise<void> {
    super.connectedCallback();
    await this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      const s = await this.api.get<{ items: StreamRow[] }>(`/portal/live-streams`);
      this.streams = s.items ?? [];
      const r = await this.api.get<{ items: RecordingRow[] }>(`/portal/recordings`);
      this.recordings = r.items ?? [];
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
  }

  private async toggleRecord(row: StreamRow, checked: boolean): Promise<void> {
    this.busy = { ...this.busy, [row.id]: true };
    try {
      await this.api.post(`/portal/live-streams/${encodeURIComponent(row.id)}/record`, {
        record_to_vod: checked,
      });
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "toggle_failed";
    } finally {
      const next = { ...this.busy };
      delete next[row.id];
      this.busy = next;
    }
  }

  render(): TemplateResult {
    return html`
      <portal-card heading="Recordings">
        ${this.error ? html`<p class="err">${this.error}</p>` : ""}
        <p class="note">
          Recording is opt-in per stream. New streams default to OFF; toggle below to enable VOD capture.
        </p>

        <section>
          <portal-data-table
            heading="Per-Stream Recording Policy"
            description="Turn recording on for new or existing live streams before they begin broadcasting."
          >
            <table>
              <thead>
                <tr><th>Stream</th><th>Record</th></tr>
              </thead>
              <tbody>
                ${this.streams.map(
                  (s) => html`<tr>
                    <td>${s.name}</td>
                    <td>
                      <input
                        type="checkbox"
                        .checked=${s.recordToVod}
                        ?disabled=${!!this.busy[s.id]}
                        @change=${(e: Event): void => {
                          void this.toggleRecord(s, (e.target as HTMLInputElement).checked);
                        }}
                      />
                    </td>
                  </tr>`,
                )}
              </tbody>
            </table>
          </portal-data-table>
        </section>

        <section>
          <portal-data-table
            heading="Recorded Sessions"
            description="Completed and in-progress VOD captures generated from your live streams."
          >
            <table>
              <thead>
                <tr><th>Stream</th><th>Status</th><th>Asset</th><th>Duration</th><th>Started</th><th>Ended</th></tr>
              </thead>
              <tbody>
                ${this.recordings.map(
                  (r) => html`<tr>
                    <td>${r.streamName}</td>
                    <td>
                      <portal-status-pill variant=${r.status === "ready" ? "success" : r.status === "failed" ? "danger" : "info"}>
                        ${r.status}
                      </portal-status-pill>
                    </td>
                    <td>
                      ${r.assetId
                        ? html`<a href="#/assets">${r.assetId}</a>`
                        : html`<span class="dim">-</span>`}
                    </td>
                    <td>${r.durationSec !== null ? r.durationSec.toFixed(1) + "s" : "-"}</td>
                    <td>${r.startedAt}</td>
                    <td>${r.endedAt ?? html`<span class="dim">active</span>`}</td>
                  </tr>`,
                )}
              </tbody>
            </table>
          </portal-data-table>
        </section>
      </portal-card>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-recordings": PortalRecordings;
  }
}
