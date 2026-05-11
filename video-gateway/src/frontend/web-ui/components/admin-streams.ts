import { html, nothing, render } from "lit";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";
import { installAdminPageStyles } from "./admin-shared.js";

interface StreamRow {
  id: string;
  name: string;
  projectId: string;
  status: string;
  sessionId: string | null;
  playbackUrl: string | null;
  startedAt: string;
  endedAt: string | null;
  viewerCount: number | null;
  recordToVod: boolean;
}

export class AdminStreams extends HTMLElement {
  private rows: StreamRow[] = [];
  private error: string | null = null;

  private api = new ApiClient({ baseUrl: "" });

  async connectedCallback(): Promise<void> {
    installAdminPageStyles();
    await this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      const out = await this.api.get<{ items: StreamRow[] }>(`/admin/live-streams`);
      this.rows = out.items ?? [];
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
    this.draw();
  }

  private async forceEnd(row: StreamRow): Promise<void> {
    if (!confirm(`Force-end stream ${row.id}? Connected publishers will be disconnected.`)) return;
    try {
      await this.api.post(`/admin/live-streams/${encodeURIComponent(row.id)}/end`);
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "force_end_failed";
      this.draw();
    }
  }

  private draw(): void {
    render(
      html`
      <portal-card heading="Live Stream Sessions">
        <portal-data-table
          heading="Active Streams"
          description="Current and ended ingest sessions, viewer count, and force-end controls."
        >
        ${this.error ? html`<p class="video-admin-page-error">${this.error}</p>` : nothing}
        <table class="video-admin-page-table">
          <thead>
            <tr><th>Name</th><th>Project</th><th>Status</th><th>Playback</th><th>Viewers</th><th>Record</th><th>Started</th><th>Ended</th><th></th></tr>
          </thead>
          <tbody>
            ${this.rows.map(
              (r) => html`<tr>
                <td>
                  <div>${r.name}</div>
                  <div class="video-admin-page-dim"><code>${r.id}</code></div>
                </td>
                <td>${r.projectId}</td>
                <td>
                  <portal-status-pill variant=${r.status === "live" ? "success" : "neutral"}>
                    ${r.status}
                  </portal-status-pill>
                </td>
                <td>
                  ${r.playbackUrl
                    ? html`<a class="video-admin-page-link" href=${r.playbackUrl}>open</a>`
                    : nothing}
                </td>
                <td>${r.viewerCount ?? "-"}</td>
                <td>${r.recordToVod ? "yes" : "no"}</td>
                <td>${r.startedAt}</td>
                <td>${r.endedAt ?? ""}</td>
                <td>
                  ${r.endedAt
                    ? nothing
                    : html`
                        <portal-action-row align="end">
                          <portal-button variant="danger" @click=${(): void => void this.forceEnd(r)}>
                            Force end
                          </portal-button>
                        </portal-action-row>
                      `}
                </td>
              </tr>`,
            )}
          </tbody>
        </table>
        </portal-data-table>
      </portal-card>
      `,
      this,
    );
  }
}

if (!customElements.get("admin-streams")) {
  customElements.define("admin-streams", AdminStreams);
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-streams": AdminStreams;
  }
}
