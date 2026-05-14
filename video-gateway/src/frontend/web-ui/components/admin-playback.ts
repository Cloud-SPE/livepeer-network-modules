import { html, nothing, render } from "lit";
import { ApiClient } from "@livepeer-network-modules/customer-portal-shared";
import { installAdminPageStyles } from "./admin-shared.js";

interface PlaybackRow {
  id: string;
  projectId: string;
  assetId: string | null;
  liveStreamId: string | null;
  policy: string;
  tokenRequired: boolean;
  createdAt: string;
}

export class AdminPlayback extends HTMLElement {
  private rows: PlaybackRow[] = [];
  private error: string | null = null;
  private saving: Record<string, boolean> = {};

  private readonly api = new ApiClient({ baseUrl: "" });

  connectedCallback(): void {
    installAdminPageStyles();
    void this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      const out = await this.api.get<{ items?: Array<Record<string, unknown>> }>("/admin/playback");
      this.rows = (out.items ?? []).map((row) => ({
        id: String(row["id"] ?? ""),
        projectId: String(row["project_id"] ?? ""),
        assetId: row["asset_id"] ? String(row["asset_id"]) : null,
        liveStreamId: row["live_stream_id"] ? String(row["live_stream_id"]) : null,
        policy: String(row["policy"] ?? ""),
        tokenRequired: Boolean(row["token_required"]),
        createdAt: String(row["created_at"] ?? ""),
      }));
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
    this.draw();
  }

  private async save(row: PlaybackRow, policy: string, tokenRequired: boolean): Promise<void> {
    this.saving = { ...this.saving, [row.id]: true };
    this.draw();
    try {
      await this.api.post(`/admin/playback/${encodeURIComponent(row.id)}/policy`, {
        policy,
        token_required: tokenRequired,
      });
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "save_failed";
      this.draw();
    } finally {
      const next = { ...this.saving };
      delete next[row.id];
      this.saving = next;
      this.draw();
    }
  }

  private draw(): void {
    render(
      html`
        <portal-card heading="Playback Policies">
          <portal-data-table
            heading="Playback Surface"
            description="Inspect playback IDs and adjust token requirements or policy posture."
          >
            ${this.error ? html`<p class="video-admin-page-error">${this.error}</p>` : nothing}
            <table class="video-admin-page-table">
              <thead>
                <tr>
                  <th>ID</th><th>Project</th><th>Target</th><th>Policy</th><th>Token required</th><th>Created</th><th></th>
                </tr>
              </thead>
              <tbody>
                ${this.rows.map((row) => this.renderRow(row))}
              </tbody>
            </table>
          </portal-data-table>
        </portal-card>
      `,
      this,
    );
  }

  private renderRow(row: PlaybackRow) {
    let policy = row.policy;
    let tokenRequired = row.tokenRequired;
    return html`
      <tr>
        <td><code>${row.id}</code></td>
        <td>${row.projectId}</td>
        <td>${row.assetId ?? row.liveStreamId ?? "—"}</td>
        <td>
          <input
            class="video-admin-page-toolbar-input"
            .value=${policy}
            @input=${(event: Event): void => {
              policy = (event.target as HTMLInputElement).value;
            }}
          />
        </td>
        <td>
          <label>
            <input
              type="checkbox"
              .checked=${tokenRequired}
              @change=${(event: Event): void => {
                tokenRequired = (event.target as HTMLInputElement).checked;
              }}
            />
            required
          </label>
        </td>
        <td>${row.createdAt}</td>
        <td>
          <portal-button
            variant="ghost"
            ?disabled=${!!this.saving[row.id]}
            @click=${(): void => void this.save(row, policy, tokenRequired)}
          >
            ${this.saving[row.id] ? "Saving." : "Save"}
          </portal-button>
        </td>
      </tr>
    `;
  }
}

if (!customElements.get("admin-playback")) {
  customElements.define("admin-playback", AdminPlayback);
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-playback": AdminPlayback;
  }
}
