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
    table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
    th, td { padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--border-1, #d4d4d8); text-align: left; }
    button { background: none; border: 1px solid var(--border-1, #d4d4d8); border-radius: 0.25rem; padding: 0.25rem 0.5rem; cursor: pointer; font-size: 0.75rem; }
    .live { color: #166534; font-weight: 600; }
    .err { color: #b91c1c; }
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
      <h2>Live stream sessions</h2>
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
              <td class=${r.status === "live" ? "live" : ""}>${r.status}</td>
              <td>${r.viewerCount ?? "-"}</td>
              <td>${r.recordToVod ? "yes" : "no"}</td>
              <td>${r.startedAt}</td>
              <td>${r.endedAt ?? ""}</td>
              <td>
                ${r.endedAt
                  ? ""
                  : html`<button @click=${(): void => void this.forceEnd(r)}>Force end</button>`}
              </td>
            </tr>`,
          )}
        </tbody>
      </table>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-streams": AdminStreams;
  }
}
