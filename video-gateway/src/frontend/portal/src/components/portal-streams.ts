import { LitElement, css, html, type TemplateResult } from "lit";
import { customElement, state } from "lit/decorators.js";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";

interface StreamRow {
  id: string;
  name: string;
  status: string;
  rtmpIngestUrl: string;
  playbackUrl: string;
  viewerCount: number | null;
  createdAt: string;
  endedAt: string | null;
}

interface CreatedStream extends StreamRow {
  sessionKey: string;
}

@customElement("portal-streams")
export class PortalStreams extends LitElement {
  @state() private rows: StreamRow[] = [];
  @state() private newName = "";
  @state() private created: CreatedStream | null = null;
  @state() private keyRevealed = false;
  @state() private error: string | null = null;

  private api = new ApiClient({ baseUrl: "" });

  static styles = css`
    :host { display: block; }
    .form { display: flex; gap: var(--space-2); }
    .form input { flex: 1; min-height: 2.75rem; }
    table { width: 100%; border-collapse: collapse; font-size: var(--font-size-sm); }
    th, td { padding: 0.7rem 0.75rem; border-bottom: 1px solid var(--border-1); text-align: left; vertical-align: top; }
    th {
      color: var(--text-3);
      font-size: var(--font-size-xs);
      font-weight: 650;
      text-transform: uppercase;
      letter-spacing: 0.1em;
    }
    tbody tr:hover { background: rgba(255, 255, 255, 0.02); }
    code {
      display: inline-block;
      background: rgba(255, 255, 255, 0.04);
      padding: 0.18rem 0.4rem;
      border: 1px solid var(--border-1);
      border-radius: 0.45rem;
      font-size: 0.75rem;
    }
    .reveal { display: flex; align-items: center; gap: 0.5rem; }
    .secret { font-family: monospace; }
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
      const out = await this.api.get<{ items: StreamRow[] }>(`/portal/live-streams`);
      this.rows = out.items ?? [];
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
  }

  private async createStream(e: Event): Promise<void> {
    e.preventDefault();
    if (!this.newName.trim()) return;
    try {
      const out = await this.api.post<CreatedStream>(`/portal/live-streams`, { name: this.newName });
      this.created = out;
      this.keyRevealed = true;
      this.newName = "";
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "create_failed";
    }
  }

  private async copyKey(): Promise<void> {
    if (!this.created) return;
    try {
      await navigator.clipboard.writeText(this.created.sessionKey);
    } catch {
      /* clipboard unavailable */
    }
  }

  private async endStream(row: StreamRow): Promise<void> {
    if (!confirm(`End stream ${row.name}? Connected viewers will be disconnected.`)) return;
    try {
      await this.api.post(`/portal/live-streams/${encodeURIComponent(row.id)}/end`);
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "end_failed";
    }
  }

  render(): TemplateResult {
    return html`
      <portal-card heading="Live streams">
        <portal-data-table
          heading="Stream Sessions"
          description="Create new stream keys, inspect playback URLs, and end live sessions."
        >
        <form class="form" slot="toolbar" @submit=${this.createStream}>
          <input
            placeholder="stream name"
            .value=${this.newName}
            @input=${(e: Event): void => {
              this.newName = (e.target as HTMLInputElement).value;
            }}
          />
          <portal-button type="submit">Create stream</portal-button>
        </form>

        ${this.created
          ? html`
              <portal-card heading="Stream created — copy session key now">
                <p>Name: <b>${this.created.name}</b></p>
                <p>RTMP ingest: <code>${this.created.rtmpIngestUrl}</code></p>
                <p>LL-HLS playback: <code>${this.created.playbackUrl}</code></p>
                <div class="reveal">
                  Session key:
                  <span class="secret">
                    ${this.keyRevealed ? this.created.sessionKey : "•••••••••••••"}
                  </span>
                </div>
                <portal-action-row>
                  <portal-button variant="ghost" @click=${(): void => void this.copyKey()}>
                    Copy
                  </portal-button>
                  <portal-button
                    variant="ghost"
                    @click=${(): void => {
                      this.keyRevealed = false;
                      this.created = null;
                    }}
                  >
                    Dismiss
                  </portal-button>
                </portal-action-row>
                <p>This key is shown once. Store it before dismissing.</p>
              </portal-card>
            `
          : ""}

        ${this.error ? html`<p class="err">${this.error}</p>` : ""}

        <table>
          <thead>
            <tr><th>Name</th><th>Status</th><th>Viewers</th><th>Playback</th><th>Started</th><th></th></tr>
          </thead>
          <tbody>
            ${this.rows.map(
              (r) => html`<tr>
                <td>${r.name}</td>
                <td>
                  <portal-status-pill variant=${r.status === "live" ? "success" : "neutral"}>
                    ${r.status}
                  </portal-status-pill>
                </td>
                <td>${r.viewerCount ?? "-"}</td>
                <td><code>${r.playbackUrl}</code></td>
                <td>${r.createdAt}</td>
                <td>
                  ${r.endedAt
                    ? "ended"
                    : html`
                        <portal-action-row align="end">
                          <portal-button variant="danger" @click=${(): void => void this.endStream(r)}>
                            End
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
    "portal-streams": PortalStreams;
  }
}
