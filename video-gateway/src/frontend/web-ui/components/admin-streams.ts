import { html, nothing, render } from "lit";
import { ApiClient } from "@livepeer-network-modules/customer-portal-shared";
import { installAdminPageStyles } from "./admin-shared.js";

interface StreamRow {
  id: string;
  name: string;
  projectId: string;
  status: string;
  sessionId: string | null;
  playbackUrl: string | null;
  brokerUrl: string | null;
  sessionKnown: boolean;
  lastSeenAt: string | null;
  idleSeconds: number | null;
  health: "healthy" | "degraded" | "stale" | "ended";
  startedAt: string;
  endedAt: string | null;
  viewerCount: number | null;
  recordToVod: boolean;
}

export class AdminStreams extends HTMLElement {
  private rows: StreamRow[] = [];
  private projectFilter = "";
  private error: string | null = null;

  private api = new ApiClient({ baseUrl: "" });

  async connectedCallback(): Promise<void> {
    installAdminPageStyles();
    await this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      const query = this.projectFilter.trim()
        ? `?project_id=${encodeURIComponent(this.projectFilter.trim())}`
        : "";
      const out = await this.api.get<{ items: StreamRow[] }>(`/admin/live-streams${query}`);
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
        <div class="video-admin-page-toolbar video-admin-page-toolbar--grow" slot="toolbar">
          <input
            class="video-admin-page-toolbar-input"
            placeholder="filter by project id"
            .value=${this.projectFilter}
            @input=${(e: Event): void => {
              this.projectFilter = (e.target as HTMLInputElement).value;
            }}
            @keydown=${(e: KeyboardEvent): void => {
              if (e.key === "Enter") void this.load();
            }}
          />
          <portal-button @click=${(): void => void this.load()}>Refresh</portal-button>
        </div>
        ${this.error ? html`<p class="video-admin-page-error">${this.error}</p>` : nothing}
        <table class="video-admin-page-table">
          <thead>
            <tr><th>Name</th><th>Project</th><th>Health</th><th>Status</th><th>Playback</th><th>Telemetry</th><th>Record</th><th>Started</th><th>Ended</th><th></th></tr>
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
                  <portal-status-pill variant=${this.healthVariant(r.health)}>
                    ${r.health}
                  </portal-status-pill>
                </td>
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
                <td>
                  <div>${r.sessionKnown ? "session attached" : "session missing"}</div>
                  <div class="video-admin-page-dim">${r.brokerUrl ?? "no broker route"}</div>
                  <div class="video-admin-page-dim">
                    last seen: ${r.lastSeenAt ?? "never"}${r.idleSeconds !== null ? ` · idle ${r.idleSeconds}s` : ""}
                  </div>
                </td>
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

  private healthVariant(value: StreamRow["health"]): "success" | "warning" | "danger" | "neutral" {
    switch (value) {
      case "healthy":
        return "success";
      case "stale":
        return "danger";
      case "degraded":
        return "warning";
      default:
        return "neutral";
    }
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
