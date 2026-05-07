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
    .form { display: flex; gap: 0.5rem; margin-bottom: 0.75rem; }
    .form input { flex: 1; padding: 0.5rem; border: 1px solid var(--border-1, #d4d4d8); border-radius: 0.375rem; }
    table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
    th, td { padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--border-1, #d4d4d8); text-align: left; }
    code { font-family: monospace; }
    pre { background: var(--surface-2, #f4f4f5); padding: 0.75rem; border-radius: 0.375rem; overflow-x: auto; font-size: 0.75rem; }
    button { background: none; border: 1px solid var(--border-1, #d4d4d8); border-radius: 0.25rem; padding: 0.25rem 0.5rem; cursor: pointer; font-size: 0.75rem; }
    .err { color: #b91c1c; }
    section { margin-top: 1rem; }
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
        <form class="form" @submit=${this.createEndpoint}>
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
                  <button @click=${(): void => void this.copySecret()}>Copy</button>
                  <button
                    @click=${(): void => {
                      this.secretRevealed = false;
                      this.created = null;
                    }}
                  >
                    Dismiss
                  </button>
                </p>
                <p>Stored hashed; never re-displayed. Rotate if leaked.</p>
              </portal-card>
            `
          : ""}

        ${this.error ? html`<p class="err">${this.error}</p>` : ""}

        <section>
          <h3>Endpoints</h3>
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
                      ? `${r.lastDeliveryStatus} at ${r.lastDeliveryAt}`
                      : "-"}
                  </td>
                  <td>${r.createdAt}</td>
                  <td>
                    <button @click=${(): void => void this.rotate(r)}>Rotate</button>
                    <button @click=${(): void => void this.deleteEndpoint(r)}>Remove</button>
                  </td>
                </tr>`,
              )}
            </tbody>
          </table>
        </section>

        <section>
          <h3>Delivery log</h3>
          <table>
            <thead>
              <tr><th>Event</th><th>Endpoint</th><th>Status</th><th>Attempts</th><th>When</th></tr>
            </thead>
            <tbody>
              ${this.deliveries.map(
                (d) => html`<tr>
                  <td>${d.eventType}</td>
                  <td>${d.endpointId}</td>
                  <td>${d.statusCode ?? "pending"}</td>
                  <td>${d.attempts}</td>
                  <td>${d.deliveredAt ?? ""}</td>
                </tr>`,
              )}
            </tbody>
          </table>
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
