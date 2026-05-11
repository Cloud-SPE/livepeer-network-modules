import { html, nothing, render } from "lit";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";
import { installAdminPageStyles } from "./admin-shared.js";

interface ProjectRow {
  id: string;
  customerId: string;
  name: string;
  createdAt: string;
  usage: {
    assets: number;
    uploads: number;
    liveStreams: number;
    webhooks: number;
  };
}

export class AdminProjects extends HTMLElement {
  private rows: ProjectRow[] = [];
  private customerFilter = "";
  private error: string | null = null;

  private readonly api = new ApiClient({ baseUrl: "" });

  connectedCallback(): void {
    installAdminPageStyles();
    void this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      const q = this.customerFilter.trim()
        ? `?customer_id=${encodeURIComponent(this.customerFilter.trim())}`
        : "";
      const out = await this.api.get<{ items?: Array<Record<string, unknown>> }>(`/admin/projects${q}`);
      this.rows = (out.items ?? []).map((row) => ({
        id: String(row["id"] ?? ""),
        customerId: String(row["customer_id"] ?? ""),
        name: String(row["name"] ?? ""),
        createdAt: String(row["created_at"] ?? ""),
        usage: {
          assets: Number((row["usage"] as Record<string, unknown> | undefined)?.["assets"] ?? 0),
          uploads: Number((row["usage"] as Record<string, unknown> | undefined)?.["uploads"] ?? 0),
          liveStreams: Number((row["usage"] as Record<string, unknown> | undefined)?.["live_streams"] ?? 0),
          webhooks: Number((row["usage"] as Record<string, unknown> | undefined)?.["webhooks"] ?? 0),
        },
      }));
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
    this.draw();
  }

  private draw(): void {
    render(
      html`
        <portal-card heading="Project Registry">
          <portal-data-table
            heading="Projects"
            description="Inspect project tenancy, ownership, and media footprint across the gateway."
          >
            <div class="video-admin-page-toolbar video-admin-page-toolbar--grow" slot="toolbar">
              <input
                class="video-admin-page-toolbar-input"
                placeholder="filter by customer id"
                .value=${this.customerFilter}
                @input=${(event: Event): void => {
                  this.customerFilter = (event.target as HTMLInputElement).value;
                }}
                @keydown=${(event: KeyboardEvent): void => {
                  if (event.key === "Enter") void this.load();
                }}
              />
              <portal-button @click=${(): void => void this.load()}>Refresh</portal-button>
            </div>
            ${this.error ? html`<p class="video-admin-page-error">${this.error}</p>` : nothing}
            <table class="video-admin-page-table">
              <thead>
                <tr>
                  <th>Name</th><th>Project ID</th><th>Customer</th><th>Created</th><th>Usage</th>
                </tr>
              </thead>
              <tbody>
                ${this.rows.map(
                  (row) => html`
                    <tr>
                      <td>${row.name}</td>
                      <td>${row.id}</td>
                      <td><a class="video-admin-page-link" href="#/customers/${row.customerId}">${row.customerId}</a></td>
                      <td>${row.createdAt}</td>
                      <td>
                        ${row.usage.assets} assets · ${row.usage.uploads} uploads ·
                        ${row.usage.liveStreams} streams · ${row.usage.webhooks} webhooks
                      </td>
                    </tr>
                  `,
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

if (!customElements.get("admin-projects")) {
  customElements.define("admin-projects", AdminProjects);
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-projects": AdminProjects;
  }
}
