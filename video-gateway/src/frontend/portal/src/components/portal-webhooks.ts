import { LitElement, css, html, type TemplateResult } from "lit";
import { customElement, state } from "lit/decorators.js";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";

interface WebhookRow {
  id: string;
  url: string;
  events: string[];
  createdAt: string;
  lastDeliveryStatus: number | null;
  lastDeliveryAt: string | null;
}

interface DeliveryRow {
  id: string;
  endpointId: string;
  eventType: string;
  statusCode: number | null;
  attempts: number;
  deliveredAt: string | null;
}

interface CreatedEndpoint extends WebhookRow {
  signingSecret: string;
}

const HMAC_SNIPPET = `// Verify livepeer-video-gateway webhook signatures
import { createHmac, timingSafeEqual } from "node:crypto";

function verify(req: Request, secret: string): boolean {
  const sig = req.headers.get("x-livepeer-signature") ?? "";
  const ts = req.headers.get("x-livepeer-timestamp") ?? "";
  const body = await req.text();
  const expected = createHmac("sha256", secret)
    .update(\`\${ts}.\${body}\`)
    .digest("hex");
  return timingSafeEqual(Buffer.from(sig), Buffer.from(expected));
}`;

@customElement("portal-webhooks")
export class PortalWebhooks extends LitElement {
  @state() private rows: WebhookRow[] = [];
  @state() private deliveries: DeliveryRow[] = [];
  @state() private newUrl = "";
  @state() private created: CreatedEndpoint | null = null;
  @state() private secretRevealed = false;
  @state() private error: string | null = null;

  private api = new ApiClient({ baseUrl: "" });

  static styles = css`
    :host { display: block; }
    .form { display: flex; gap: var(--space-2); flex: 1; }
    .form input { flex: 1; min-height: 2.75rem; }
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
    code { font-family: monospace; }
    pre {
      background: rgba(255, 255, 255, 0.03);
      border: 1px solid var(--border-1);
      padding: 0.9rem;
      border-radius: var(--radius-lg);
      overflow-x: auto;
      font-size: 0.75rem;
      color: var(--text-2);
    }
    .err { color: var(--danger); }
    section { margin-top: var(--space-5); }
    .dim { color: var(--text-3); }
  `;

  override async connectedCallback(): Promise<void> {
    super.connectedCallback();
    await this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      const out = await this.api.get<{ items: WebhookRow[] }>(`/portal/webhooks`);
      this.rows = out.items ?? [];
      const del = await this.api.get<{ items: DeliveryRow[] }>(`/portal/webhook-deliveries`);
      this.deliveries = del.items ?? [];
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
  }

  private async createEndpoint(e: Event): Promise<void> {
    e.preventDefault();
    if (!this.newUrl.trim()) return;
    try {
      const out = await this.api.post<CreatedEndpoint>(`/portal/webhooks`, {
        url: this.newUrl,
        events: ["asset.ready", "stream.started", "stream.ended", "recording.ready"],
      });
      this.created = out;
      this.secretRevealed = true;
      this.newUrl = "";
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "create_failed";
    }
  }

  private async rotate(row: WebhookRow): Promise<void> {
    if (!confirm(`Rotate signing secret for ${row.url}? Existing receivers must update.`)) return;
    try {
      const out = await this.api.post<CreatedEndpoint>(
        `/portal/webhooks/${encodeURIComponent(row.id)}/rotate`,
      );
      this.created = out;
      this.secretRevealed = true;
    } catch (err) {
      this.error = err instanceof Error ? err.message : "rotate_failed";
    }
  }

  private async deleteEndpoint(row: WebhookRow): Promise<void> {
    if (!confirm(`Remove webhook endpoint ${row.url}?`)) return;
    try {
      await this.api.request("DELETE", `/portal/webhooks/${encodeURIComponent(row.id)}`);
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "remove_failed";
    }
  }

  private async copySecret(): Promise<void> {
    if (!this.created) return;
    try {
      await navigator.clipboard.writeText(this.created.signingSecret);
    } catch {
      /* clipboard unavailable */
    }
  }

  render(): TemplateResult {
    return html`
      <portal-card heading="Webhooks">
        <portal-data-table
          heading="Endpoint Registry"
          description="Create webhook endpoints, rotate signing secrets, and monitor recent deliveries."
        >
          <form class="form" slot="toolbar" @submit=${this.createEndpoint}>
            <input
              placeholder="https://example.com/webhook"
              .value=${this.newUrl}
              @input=${(e: Event): void => {
                this.newUrl = (e.target as HTMLInputElement).value;
              }}
            />
            <portal-button type="submit">Add endpoint</portal-button>
          </form>

          ${this.created
            ? html`
                <portal-card heading="Signing secret — copy now (one-time reveal)">
                  <p>URL: <code>${this.created.url}</code></p>
                  <p>
                    Secret:
                    <code>${this.secretRevealed ? this.created.signingSecret : "•••••••••••••"}</code>
                  </p>
                  <portal-action-row>
                    <portal-button variant="ghost" @click=${(): void => void this.copySecret()}>
                      Copy
                    </portal-button>
                    <portal-button
                      variant="ghost"
                      @click=${(): void => {
                        this.secretRevealed = false;
                        this.created = null;
                      }}
                    >
                      Dismiss
                    </portal-button>
                  </portal-action-row>
                  <p>Stored hashed; never re-displayed. Rotate if leaked.</p>
                </portal-card>
              `
            : ""}

          ${this.error ? html`<p class="err">${this.error}</p>` : ""}
          <table>
            <thead>
              <tr><th>URL</th><th>Events</th><th>Last delivery</th><th>Created</th><th></th></tr>
            </thead>
            <tbody>
              ${this.rows.map(
                (r) => html`<tr>
                  <td><code>${r.url}</code></td>
                  <td>${r.events.join(", ")}</td>
                  <td>
                    ${r.lastDeliveryStatus !== null
                      ? html`
                          <portal-status-pill variant=${r.lastDeliveryStatus >= 500 ? "danger" : r.lastDeliveryStatus >= 400 ? "warning" : "success"}>
                            ${r.lastDeliveryStatus}
                          </portal-status-pill>
                          <span class="dim"> at ${r.lastDeliveryAt}</span>
                        `
                      : html`<span class="dim">-</span>`}
                  </td>
                  <td>${r.createdAt}</td>
                  <td>
                    <portal-action-row align="end">
                      <portal-button variant="ghost" @click=${(): void => void this.rotate(r)}>
                        Rotate
                      </portal-button>
                      <portal-button variant="danger" @click=${(): void => void this.deleteEndpoint(r)}>
                        Remove
                      </portal-button>
                    </portal-action-row>
                  </td>
                </tr>`,
              )}
            </tbody>
          </table>
        </portal-data-table>

        <section>
          <portal-data-table
            heading="Delivery Log"
            description="Recent webhook deliveries with status codes and retry counts."
          >
            <table>
              <thead>
                <tr><th>Event</th><th>Endpoint</th><th>Status</th><th>Attempts</th><th>When</th></tr>
              </thead>
              <tbody>
                ${this.deliveries.map(
                  (d) => html`<tr>
                    <td>${d.eventType}</td>
                    <td>${d.endpointId}</td>
                    <td>
                      <portal-status-pill variant=${d.statusCode === null ? "neutral" : d.statusCode >= 500 ? "danger" : d.statusCode >= 400 ? "warning" : "success"}>
                        ${d.statusCode ?? "pending"}
                      </portal-status-pill>
                    </td>
                    <td>${d.attempts}</td>
                    <td>${d.deliveredAt ?? html`<span class="dim">queued</span>`}</td>
                  </tr>`,
                )}
              </tbody>
            </table>
          </portal-data-table>
        </section>

        <section>
          <h3>HMAC verification example</h3>
          <pre>${HMAC_SNIPPET}</pre>
        </section>
      </portal-card>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-webhooks": PortalWebhooks;
  }
}
