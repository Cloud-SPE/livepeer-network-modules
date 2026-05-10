import { html, nothing, render } from "lit";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";
import { installAdminPageStyles } from "./admin-shared.js";

interface ResolverCandidate {
  brokerUrl: string;
  capability: string;
  offering: string;
  ethAddress: string;
  pricePerWorkUnitWei: string;
  extra: unknown;
  constraints: unknown;
}

export class AdminNodes extends HTMLElement {
  private rows: ResolverCandidate[] = [];
  private loading = false;
  private error: string | null = null;
  private selectedBrokerUrl: string | null = null;
  private readonly api = new ApiClient({ baseUrl: "" });

  async connectedCallback(): Promise<void> {
    installAdminPageStyles();
    this.selectedBrokerUrl = this.getAttribute("selectedBrokerUrl");
    this.draw();
    await this.load();
  }

  attributeChangedCallback(): void {
    this.selectedBrokerUrl = this.getAttribute("selectedBrokerUrl");
    this.draw();
  }

  static get observedAttributes(): string[] {
    return ["selectedBrokerUrl"];
  }

  private async load(): Promise<void> {
    this.loading = true;
    this.error = null;
    this.draw();
    try {
      const out = await this.api.get<{ candidates: ResolverCandidate[] }>("/admin/video/resolver-candidates");
      this.rows = out.candidates ?? [];
    } catch (err) {
      this.error = err instanceof Error ? err.message : "resolver_candidates_failed";
      this.rows = [];
    } finally {
      this.loading = false;
      this.draw();
    }
  }

  private draw(): void {
    const selected =
      this.selectedBrokerUrl === null
        ? null
        : this.rows.find((row) => row.brokerUrl === this.selectedBrokerUrl) ?? null;

    render(
      html`
        <div class="video-admin-page-split">
          <portal-data-table heading="Nodes" description="Resolver candidates currently visible to the video gateway.">
            ${this.error ? html`<p class="video-admin-page-error">${this.error}</p>` : nothing}
            ${this.loading ? html`<p class="video-admin-page-note">Loading.</p>` : nothing}
            <table class="video-admin-page-table">
              <thead>
                <tr>
                  <th>Broker URL</th>
                  <th>Capability</th>
                  <th>Offering</th>
                  <th>Price / unit</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                ${this.rows.map(
                  (row) => html`
                    <tr>
                      <td><code>${row.brokerUrl}</code></td>
                      <td>${row.capability}</td>
                      <td>${row.offering}</td>
                      <td>${row.pricePerWorkUnitWei}</td>
                      <td>
                        <portal-button variant="ghost" @click=${() => this.open(row.brokerUrl)}>Open</portal-button>
                      </td>
                    </tr>
                  `,
                )}
              </tbody>
            </table>
          </portal-data-table>

          <portal-card heading="Node detail" subheading="Selected resolver candidate route metadata.">
            ${selected
              ? html`
                  <dl class="video-admin-page-meta-list">
                    <div class="video-admin-page-meta-item">
                      <dt>Broker URL</dt>
                      <dd><code>${selected.brokerUrl}</code></dd>
                    </div>
                    <div class="video-admin-page-meta-item">
                      <dt>ETH address</dt>
                      <dd><code>${selected.ethAddress || "—"}</code></dd>
                    </div>
                    <div class="video-admin-page-meta-item">
                      <dt>Capability</dt>
                      <dd>${selected.capability}</dd>
                    </div>
                    <div class="video-admin-page-meta-item">
                      <dt>Offering</dt>
                      <dd>${selected.offering}</dd>
                    </div>
                    <div class="video-admin-page-meta-item">
                      <dt>Price / unit</dt>
                      <dd>${selected.pricePerWorkUnitWei}</dd>
                    </div>
                    <div class="video-admin-page-meta-item">
                      <dt>Constraints</dt>
                      <dd><pre class="video-admin-page-pre">${JSON.stringify(selected.constraints, null, 2)}</pre></dd>
                    </div>
                    <div class="video-admin-page-meta-item">
                      <dt>Extra</dt>
                      <dd><pre class="video-admin-page-pre">${JSON.stringify(selected.extra, null, 2)}</pre></dd>
                    </div>
                  </dl>
                `
              : html`<p class="video-admin-page-note">Select a node to inspect routing metadata.</p>`}
          </portal-card>
        </div>
      `,
      this,
    );
  }

  private open(brokerUrl: string): void {
    window.location.hash = `#/nodes/${encodeURIComponent(brokerUrl)}`;
  }
}

if (!customElements.get("admin-nodes")) {
  customElements.define("admin-nodes", AdminNodes);
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-nodes": AdminNodes;
  }
}
