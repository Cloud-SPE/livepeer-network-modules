import { LitElement, css, html, type TemplateResult } from "lit";
import { customElement, state } from "lit/decorators.js";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";

interface StreamRow {
  id: string;
  projectId: string;
  status: string;
  startedAt: string;
  endedAt: string | null;
  viewerCount: number | null;
  recordToVod: boolean;
}

@customElement("admin-streams")
export class AdminStreams extends LitElement {
  @state() private rows: StreamRow[] = [];
  @state() private error: string | null = null;

  private api = new ApiClient({ baseUrl: "" });

  static styles = css`
    :host { display: block; }
    table { width: 100%; border-collapse: collapse; font-size: var(--font-size-sm); }
    th, td { padding: 0.75rem 0.8rem; border-bottom: 1px solid var(--border-1); text-align: left; }
    th {
      color: var(--text-3);
      font-size: var(--font-size-xs);
      font-weight: 650;
      text-transform: uppercase;
      letter-spacing: 0.1em;
    }
    tbody tr:hover { background: rgba(255,255,255,0.02); }
    .live { color: var(--success); font-weight: 650; }
    .err { color: var(--danger); }
  `;

  override async connectedCallback(): Promise<void> {
    super.connectedCallback();
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
  }

  private async forceEnd(row: StreamRow): Promise<void> {
    if (!confirm(`Force-end stream ${row.id}? Connected publishers will be disconnected.`)) return;
    try {
      await this.api.post(`/admin/live-streams/${encodeURIComponent(row.id)}/end`);
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "force_end_failed";
    }
  }

  render(): TemplateResult {
    return html`
      <portal-card heading="Live Stream Sessions">
        <portal-data-table
          heading="Active Streams"
          description="Current and ended ingest sessions, viewer count, and force-end controls."
        >
        ${this.error ? html`<p class="err">${this.error}</p>` : ""}
        <table>
          <thead>
            <tr><th>ID</th><th>Project</th><th>Status</th><th>Viewers</th><th>Record</th><th>Started</th><th>Ended</th><th></th></tr>
          </thead>
          <tbody>
            ${this.rows.map(
              (r) => html`<tr>
                <td>${r.id}</td>
                <td>${r.projectId}</td>
                <td>
                  <portal-status-pill variant=${r.status === "live" ? "success" : "neutral"}>
                    ${r.status}
                  </portal-status-pill>
                </td>
                <td>${r.viewerCount ?? "-"}</td>
                <td>${r.recordToVod ? "yes" : "no"}</td>
                <td>${r.startedAt}</td>
                <td>${r.endedAt ?? ""}</td>
                <td>
                  ${r.endedAt
                    ? ""
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
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-streams": AdminStreams;
  }
}
