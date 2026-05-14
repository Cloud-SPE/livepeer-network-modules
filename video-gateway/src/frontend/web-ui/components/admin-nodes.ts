import { html, nothing, render } from "lit";
import { ApiClient } from "@livepeer-network-modules/customer-portal-shared";
import { installAdminPageStyles } from "./admin-shared.js";

interface ResolverCandidate {
  brokerUrl: string;
  capability: string;
  offering: string;
  ethAddress: string;
  pricePerWorkUnitWei: string;
  extra: unknown;
  constraints: unknown;
  suppressed?: boolean;
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

  private async toggleSuppression(row: ResolverCandidate): Promise<void> {
    try {
      await this.api.post(
        row.suppressed
          ? "/admin/video/route-controls/unsuppress"
          : "/admin/video/route-controls/suppress",
        { broker_url: row.brokerUrl },
      );
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "route_control_failed";
      this.draw();
    }
  }

  private draw(): void {
    const selected =
      this.selectedBrokerUrl === null
        ? null
        : this.rows.find((row) => row.brokerUrl === this.selectedBrokerUrl) ?? null;
    const abrCount = this.rows.filter((row) => row.capability === "video:transcode.abr").length;
    const liveCount = this.rows.filter((row) => row.capability === "video:live.rtmp").length;
    const suppressedCount = this.rows.filter((row) => row.suppressed).length;

    render(
      html`
        <div class="video-admin-page-split">
          <portal-data-table heading="Nodes" description="Resolver candidates currently visible to the video gateway.">
            <div class="video-admin-page-toolbar" slot="toolbar">
              <span>ABR: ${abrCount}</span>
              <span>Live: ${liveCount}</span>
              <span>Suppressed: ${suppressedCount}</span>
            </div>
            ${this.error ? html`<p class="video-admin-page-error">${this.error}</p>` : nothing}
            ${this.loading ? html`<p class="video-admin-page-note">Loading.</p>` : nothing}
            <table class="video-admin-page-table">
              <thead>
                <tr>
                  <th>Broker URL</th>
                  <th>Capability</th>
                  <th>Offering</th>
                  <th>Price / unit</th>
                  <th>Status</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                ${this.rows.map(
                  (row) => html`
                    <tr>
                      <td><code>${row.brokerUrl}</code></td>
                      <td>${this.capabilityLabel(row.capability)}</td>
                      <td>${row.offering}</td>
                      <td>${row.pricePerWorkUnitWei}</td>
                      <td>
                        <portal-status-pill variant=${row.suppressed ? "warning" : "success"}>
                          ${row.suppressed ? "suppressed" : "active"}
                        </portal-status-pill>
                      </td>
                      <td>
                        <portal-action-row align="end">
                          <portal-button variant="ghost" @click=${() => this.open(row.brokerUrl)}>Open</portal-button>
                          <portal-button variant=${row.suppressed ? "ghost" : "danger"} @click=${() => void this.toggleSuppression(row)}>
                            ${row.suppressed ? "Unsuppress" : "Suppress"}
                          </portal-button>
                        </portal-action-row>
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
                      <dd>${this.capabilityLabel(selected.capability)}</dd>
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
                      <dt>Status</dt>
                      <dd>${selected.suppressed ? "Suppressed in gateway selector" : "Eligible for selection"}</dd>
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

  private capabilityLabel(value: string): string {
    if (value === "video:transcode.abr") return `${value} (VOD and ladder jobs)`;
    return value;
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
