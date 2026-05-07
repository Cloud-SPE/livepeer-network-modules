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
    .form { display: flex; gap: 0.5rem; margin-bottom: 0.75rem; }
    .form input { flex: 1; padding: 0.5rem; border: 1px solid var(--border-1, #d4d4d8); border-radius: 0.375rem; }
    table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
    th, td { padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--border-1, #d4d4d8); text-align: left; vertical-align: top; }
    code { background: var(--surface-2, #f4f4f5); padding: 0.1rem 0.3rem; border-radius: 0.25rem; font-size: 0.75rem; }
    .reveal { display: flex; align-items: center; gap: 0.5rem; }
    .secret { font-family: monospace; }
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
        <form class="form" @submit=${this.createStream}>
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
                  <button @click=${(): void => void this.copyKey()}>Copy</button>
                  <button
                    @click=${(): void => {
                      this.keyRevealed = false;
                      this.created = null;
                    }}
                  >
                    Dismiss
                  </button>
                </div>
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
                <td class=${r.status === "live" ? "live" : ""}>${r.status}</td>
                <td>${r.viewerCount ?? "-"}</td>
                <td><code>${r.playbackUrl}</code></td>
                <td>${r.createdAt}</td>
                <td>
                  ${r.endedAt
                    ? "ended"
                    : html`<button @click=${(): void => void this.endStream(r)}>End</button>`}
                </td>
              </tr>`,
            )}
          </tbody>
        </table>
      </portal-card>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-streams": PortalStreams;
  }
}
