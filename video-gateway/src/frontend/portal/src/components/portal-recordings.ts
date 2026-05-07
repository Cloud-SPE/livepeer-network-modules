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
    section { margin-top: 1rem; }
    table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
    th, td { padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--border-1, #d4d4d8); text-align: left; }
    a { color: var(--accent-1, #2563eb); }
    .err { color: #b91c1c; }
    .note { color: var(--text-3, #71717a); font-size: 0.75rem; }
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
          <h3>Stream record_to_vod toggle</h3>
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
        </section>

        <section>
          <h3>Recorded sessions</h3>
          <table>
            <thead>
              <tr><th>Stream</th><th>Status</th><th>Asset</th><th>Duration</th><th>Started</th><th>Ended</th></tr>
            </thead>
            <tbody>
              ${this.recordings.map(
                (r) => html`<tr>
                  <td>${r.streamName}</td>
                  <td>${r.status}</td>
                  <td>
                    ${r.assetId
                      ? html`<a href="#/assets">${r.assetId}</a>`
                      : "-"}
                  </td>
                  <td>${r.durationSec !== null ? r.durationSec.toFixed(1) + "s" : "-"}</td>
                  <td>${r.startedAt}</td>
                  <td>${r.endedAt ?? ""}</td>
                </tr>`,
              )}
            </tbody>
          </table>
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
