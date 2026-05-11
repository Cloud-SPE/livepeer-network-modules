import { html, nothing, render } from "lit";
import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";
import type { PortalApiKeys } from "@livepeer-rewrite/customer-portal-shared";
import { installAdminPageStyles } from "./admin-shared.js";

interface CustomerSummary {
  id: string;
  email: string;
  tier: string;
  status: string;
  balanceCents: number;
  reservedCents: number;
}

interface CredentialSummary {
  id: string;
  label: string | null;
  createdAt: string;
  lastUsedAt: string | null;
  revokedAt: string | null;
}

interface TopupRow {
  id: string;
  customerId: string;
  stripeSessionId: string;
  amountUsdCents: number;
  status: string;
  createdAt: string;
  refundedAt: string | null;
}

interface ReservationRow {
  id: string;
  kind: string;
  state: string;
  capability: string | null;
  model: string | null;
  amountUsdCents: number | null;
  committedUsdCents: number | null;
  refundedUsdCents: number | null;
  createdAt: string;
  resolvedAt: string | null;
}

interface UsageRow {
  id: string;
  capability: string;
  amountCents: number;
  createdAt: string;
  workId: string | null;
  assetId: string | null;
  liveStreamId: string | null;
  charge: {
    state: string | null;
    estimatedAmountCents: number | null;
    committedAmountCents: number | null;
    refundedAmountCents: number | null;
  } | null;
}

interface UsageSummary {
  topupTotalCents: number;
  usageCommittedCents: number;
  reservedOpenCents: number;
  refundedCents: number;
}

interface AuditRow {
  ts: string;
  actor: string;
  action: string;
  targetType: string;
  targetId: string | null;
  detail: string;
}

export class AdminCustomerDetail extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["customerid"];
  }

  private customer: CustomerSummary | null = null;
  private authTokens: CredentialSummary[] = [];
  private apiKeys: CredentialSummary[] = [];
  private topups: TopupRow[] = [];
  private reservations: ReservationRow[] = [];
  private usage: UsageRow[] = [];
  private usageSummary: UsageSummary | null = null;
  private audit: AuditRow[] = [];
  private error: string | null = null;
  private issuingAuthToken = false;
  private createdAuthToken = "";

  private api = new ApiClient({ baseUrl: "" });

  connectedCallback(): void {
    installAdminPageStyles();
    this.draw();
    void this.load();
  }

  attributeChangedCallback(): void {
    if (this.isConnected) {
      void this.load();
    }
  }

  get customerId(): string {
    return this.getAttribute("customerId") ?? "";
  }

  set customerId(value: string) {
    this.setAttribute("customerId", value);
  }

  private get apiKeysWidget(): PortalApiKeys | null {
    return this.querySelector("#api-keys") as PortalApiKeys | null;
  }

  private async load(): Promise<void> {
    if (!this.customerId) return;
    this.error = null;
    this.draw();
    try {
      const [customerOut, authTokensOut, apiKeysOut, topupsOut, reservationsOut, usageOut, auditOut] = await Promise.all([
        this.api.get<{ customer: Record<string, unknown> }>(`/admin/customers/${encodeURIComponent(this.customerId)}`),
        this.api.get<{ auth_tokens?: Array<Record<string, unknown>> }>(
          `/admin/customers/${encodeURIComponent(this.customerId)}/auth-tokens`,
        ),
        this.api.get<{ api_keys?: Array<Record<string, unknown>> }>(
          `/admin/customers/${encodeURIComponent(this.customerId)}/api-keys`,
        ),
        this.api.get<{ topups?: Array<Record<string, unknown>> }>(
          `/admin/topups?customer_id=${encodeURIComponent(this.customerId)}&limit=25`,
        ),
        this.api.get<{ reservations?: Array<Record<string, unknown>> }>(
          `/admin/reservations?customer_id=${encodeURIComponent(this.customerId)}&limit=25`,
        ),
        this.api.get<{ items?: Array<Record<string, unknown>>; summary?: Record<string, unknown> }>(
          `/admin/usage?customer_id=${encodeURIComponent(this.customerId)}&limit=50`,
        ),
        this.api.get<{ events?: Array<Record<string, unknown>> }>(`/admin/audit?limit=100`),
      ]);

      this.customer = this.mapCustomer(customerOut.customer);
      this.authTokens = (authTokensOut.auth_tokens ?? []).map((row) => this.mapCredential(row));
      this.apiKeys = (apiKeysOut.api_keys ?? []).map((row) => this.mapCredential(row));
      this.topups = (topupsOut.topups ?? []).map((row) => this.mapTopup(row));
      this.reservations = (reservationsOut.reservations ?? []).map((row) => this.mapReservation(row));
      this.usage = (usageOut.items ?? []).map((row) => this.mapUsage(row));
      this.usageSummary = usageOut.summary ? this.mapUsageSummary(usageOut.summary) : null;
      this.audit = (auditOut.events ?? [])
        .map((row) => this.mapAudit(row))
        .filter((row) => row.targetId === this.customerId || row.detail.includes(this.customerId));
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
    this.draw();
  }

  private async issueAuthToken(): Promise<void> {
    this.issuingAuthToken = true;
    this.error = null;
    this.draw();
    try {
      const out = await this.api.post<{ auth_token?: string }>(
        `/admin/customers/${encodeURIComponent(this.customerId)}/auth-tokens`,
        {},
      );
      await this.load();
      this.createdAuthToken = out.auth_token ?? "";
    } catch (err) {
      this.error = err instanceof Error ? err.message : "issue_auth_token_failed";
    } finally {
      this.issuingAuthToken = false;
      this.draw();
    }
  }

  private async revokeAuthToken(id: string): Promise<void> {
    try {
      await this.api.request(
        "DELETE",
        `/admin/customers/${encodeURIComponent(this.customerId)}/auth-tokens/${encodeURIComponent(id)}`,
      );
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "revoke_auth_token_failed";
      this.draw();
    }
  }

  private async issueApiKey(event: CustomEvent<{ label?: string }>): Promise<void> {
    try {
      const widget = this.apiKeysWidget;
      const out = await this.api.post<{ api_key?: string }>(
        `/admin/customers/${encodeURIComponent(this.customerId)}/api-keys`,
        event.detail.label ? { label: event.detail.label } : {},
      );
      await this.load();
      if (out.api_key) widget?.showPlaintext(out.api_key);
    } catch (err) {
      this.error = err instanceof Error ? err.message : "issue_api_key_failed";
      this.draw();
    }
  }

  private async revokeApiKey(event: CustomEvent<{ id: string }>): Promise<void> {
    try {
      await this.api.request(
        "DELETE",
        `/admin/customers/${encodeURIComponent(this.customerId)}/api-keys/${encodeURIComponent(event.detail.id)}`,
      );
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "revoke_api_key_failed";
      this.draw();
    }
  }

  private mapCustomer(row: Record<string, unknown>): CustomerSummary {
    return {
      id: String(row["id"] ?? ""),
      email: String(row["email"] ?? ""),
      tier: String(row["tier"] ?? ""),
      status: String(row["status"] ?? ""),
      balanceCents: parseInt(String(row["balance_usd_cents"] ?? "0"), 10) || 0,
      reservedCents: parseInt(String(row["reserved_usd_cents"] ?? "0"), 10) || 0,
    };
  }

  private mapCredential(row: Record<string, unknown>): CredentialSummary {
    return {
      id: String(row["id"] ?? ""),
      label: row["label"] ? String(row["label"]) : null,
      createdAt: String(row["created_at"] ?? ""),
      lastUsedAt: row["last_used_at"] ? String(row["last_used_at"]) : null,
      revokedAt: row["revoked_at"] ? String(row["revoked_at"]) : null,
    };
  }

  private mapTopup(row: Record<string, unknown>): TopupRow {
    return {
      id: String(row["id"] ?? ""),
      customerId: String(row["customer_id"] ?? ""),
      stripeSessionId: String(row["stripe_session_id"] ?? ""),
      amountUsdCents: parseInt(String(row["amount_usd_cents"] ?? "0"), 10) || 0,
      status: String(row["status"] ?? ""),
      createdAt: String(row["created_at"] ?? ""),
      refundedAt: row["refunded_at"] ? String(row["refunded_at"]) : null,
    };
  }

  private mapReservation(row: Record<string, unknown>): ReservationRow {
    const parseMaybe = (key: string): number | null => {
      const value = row[key];
      if (value === null || value === undefined) return null;
      const parsed = parseInt(String(value), 10);
      return Number.isFinite(parsed) ? parsed : null;
    };
    return {
      id: String(row["id"] ?? ""),
      kind: String(row["kind"] ?? ""),
      state: String(row["state"] ?? ""),
      capability: row["capability"] ? String(row["capability"]) : null,
      model: row["model"] ? String(row["model"]) : null,
      amountUsdCents: parseMaybe("amount_usd_cents"),
      committedUsdCents: parseMaybe("committed_usd_cents"),
      refundedUsdCents: parseMaybe("refunded_usd_cents"),
      createdAt: String(row["created_at"] ?? ""),
      resolvedAt: row["resolved_at"] ? String(row["resolved_at"]) : null,
    };
  }

  private mapAudit(row: Record<string, unknown>): AuditRow {
    return {
      ts: String(row["ts"] ?? ""),
      actor: String(row["actor"] ?? ""),
      action: String(row["action"] ?? ""),
      targetType: String(row["targetType"] ?? row["target_type"] ?? ""),
      targetId: row["targetId"] ? String(row["targetId"]) : row["target_id"] ? String(row["target_id"]) : null,
      detail: String(row["detail"] ?? ""),
    };
  }

  private mapUsage(row: Record<string, unknown>): UsageRow {
    return {
      id: String(row["id"] ?? ""),
      capability: String(row["capability"] ?? ""),
      amountCents: parseInt(String(row["amount_cents"] ?? "0"), 10) || 0,
      createdAt: String(row["created_at"] ?? ""),
      workId: row["work_id"] ? String(row["work_id"]) : null,
      assetId: row["asset_id"] ? String(row["asset_id"]) : null,
      liveStreamId: row["live_stream_id"] ? String(row["live_stream_id"]) : null,
      charge:
        row["charge"] && typeof row["charge"] === "object"
          ? {
              state:
                (row["charge"] as Record<string, unknown>)["state"] !== undefined
                  ? String((row["charge"] as Record<string, unknown>)["state"] ?? "")
                  : null,
              estimatedAmountCents: parseMaybeNumber(
                (row["charge"] as Record<string, unknown>)["estimated_amount_cents"],
              ),
              committedAmountCents: parseMaybeNumber(
                (row["charge"] as Record<string, unknown>)["committed_amount_cents"],
              ),
              refundedAmountCents: parseMaybeNumber(
                (row["charge"] as Record<string, unknown>)["refunded_amount_cents"],
              ),
            }
          : null,
    };
  }

  private mapUsageSummary(row: Record<string, unknown>): UsageSummary {
    return {
      topupTotalCents: parseMaybeNumber(row["topup_total_cents"]) ?? 0,
      usageCommittedCents: parseMaybeNumber(row["usage_committed_cents"]) ?? 0,
      reservedOpenCents: parseMaybeNumber(row["reserved_open_cents"]) ?? 0,
      refundedCents: parseMaybeNumber(row["refunded_cents"]) ?? 0,
    };
  }

  private draw(): void {
    if (this.error) {
      render(html`<p class="video-admin-page-error">${this.error}</p>`, this);
      return;
    }
    if (!this.customer) {
      render(html`<p>Loading.</p>`, this);
      return;
    }
    const d = this.customer;
    render(
      html`
      <portal-card heading="Customer ${d.id}">
        <div class="video-admin-page-summary">
          <p>${d.email}</p>
          <p>
            Tier: <b>${d.tier}</b>
            &middot;
            Status: <b>${d.status}</b>
          </p>
          <p>
            Balance: <b class="video-admin-page-money">$${(d.balanceCents / 100).toFixed(2)}</b>
            &middot;
            Reserved: <b class="video-admin-page-money">$${(d.reservedCents / 100).toFixed(2)}</b>
          </p>
        </div>
        <portal-action-row>
          <portal-button
            variant="ghost"
            @click=${(): void => {
              window.location.hash = `#/customers/${d.id}/adjust`;
            }}
          >
            Adjust balance
          </portal-button>
          <portal-button
            variant="danger"
            @click=${(): void => {
              window.location.hash = `#/customers/${d.id}/refund`;
            }}
          >
            Refund
          </portal-button>
        </portal-action-row>

        <div class="video-admin-page-split">
          <portal-detail-section
            heading="UI auth tokens"
            description="Browser-login credentials for this customer. New tokens are revealed once."
          >
            <div class="video-admin-page-plaintext">
              ${this.createdAuthToken || "Issue a new token to reveal it here once."}
            </div>
            <div class="video-admin-page-token-issue">
              <portal-button @click=${(): void => void this.issueAuthToken()} ?disabled=${this.issuingAuthToken}>
                ${this.issuingAuthToken ? "Issuing." : "Issue auth token"}
              </portal-button>
            </div>
            <table class="video-admin-page-table">
              <thead><tr><th>Label</th><th>Created</th><th>Last used</th><th>Status</th><th></th></tr></thead>
              <tbody>
                ${this.authTokens.map(
                  (token) => html`<tr>
                    <td>${token.label ?? "(unlabeled)"}</td>
                    <td>${token.createdAt}</td>
                    <td>${token.lastUsedAt ?? "—"}</td>
                    <td>${token.revokedAt ? "Revoked" : "Active"}</td>
                    <td>
                      ${token.revokedAt
                        ? nothing
                        : html`
                            <portal-button variant="danger" @click=${(): void => void this.revokeAuthToken(token.id)}>
                              Revoke
                            </portal-button>
                          `}
                    </td>
                  </tr>`,
                )}
              </tbody>
            </table>
          </portal-detail-section>

          <portal-detail-section
            heading="Product API keys"
            description="Usage credentials for ingest, playback, and other customer-facing product traffic."
          >
            <portal-api-keys
              id="api-keys"
              .keys=${this.apiKeys}
              @portal-api-key-issue=${(event: CustomEvent<{ label?: string }>): void => {
                void this.issueApiKey(event);
              }}
              @portal-api-key-revoke=${(event: CustomEvent<{ id: string }>): void => {
                void this.revokeApiKey(event);
              }}
            ></portal-api-keys>
          </portal-detail-section>

          <portal-detail-section
            heading="Top-ups"
            description="Customer prepaid-balance history, including Stripe session ids used for refunds."
          >
            <table class="video-admin-page-table">
              <thead><tr><th>ID</th><th>Stripe session</th><th>Amount</th><th>Status</th><th>When</th></tr></thead>
              <tbody>
                ${this.topups.map(
                  (t) => html`<tr>
                    <td>${t.id}</td>
                    <td>${t.stripeSessionId}</td>
                    <td class="video-admin-page-money">$${(t.amountUsdCents / 100).toFixed(2)}</td>
                    <td>${t.status}</td>
                    <td>${t.createdAt}</td>
                  </tr>`,
                )}
              </tbody>
            </table>
          </portal-detail-section>

          <portal-detail-section
            heading="Reservations"
            description="Reserved, committed, and refunded customer funds by work item."
          >
            <table class="video-admin-page-table">
              <thead><tr><th>ID</th><th>Kind</th><th>State</th><th>Capability</th><th>Model</th><th>Amount</th><th>Committed</th><th>Resolved</th></tr></thead>
              <tbody>
                ${this.reservations.map(
                  (r) => html`<tr>
                    <td>${r.id}</td>
                    <td>${r.kind}</td>
                    <td>${r.state}</td>
                    <td>${r.capability ?? "—"}</td>
                    <td>${r.model ?? "—"}</td>
                    <td class="video-admin-page-money">${r.amountUsdCents !== null ? `$${(r.amountUsdCents / 100).toFixed(2)}` : "—"}</td>
                    <td class="video-admin-page-money">${r.committedUsdCents !== null ? `$${(r.committedUsdCents / 100).toFixed(2)}` : "—"}</td>
                    <td>${r.resolvedAt ?? "—"}</td>
                  </tr>`,
                )}
              </tbody>
            </table>
          </portal-detail-section>

          <portal-detail-section
            heading="Usage"
            description="Committed gateway charges by media workload."
          >
            ${this.usageSummary
              ? html`
                  <dl class="video-admin-page-meta-list">
                    <div class="video-admin-page-meta-item"><dt>Top-ups</dt><dd class="video-admin-page-money">$${(this.usageSummary.topupTotalCents / 100).toFixed(2)}</dd></div>
                    <div class="video-admin-page-meta-item"><dt>Committed media usage</dt><dd class="video-admin-page-money">$${(this.usageSummary.usageCommittedCents / 100).toFixed(2)}</dd></div>
                    <div class="video-admin-page-meta-item"><dt>Open media reservations</dt><dd class="video-admin-page-money">$${(this.usageSummary.reservedOpenCents / 100).toFixed(2)}</dd></div>
                    <div class="video-admin-page-meta-item"><dt>Refunded media reservations</dt><dd class="video-admin-page-money">$${(this.usageSummary.refundedCents / 100).toFixed(2)}</dd></div>
                  </dl>
                `
              : nothing}
            <table class="video-admin-page-table">
              <thead><tr><th>ID</th><th>Capability</th><th>Target</th><th>State</th><th>Estimated</th><th>Committed</th><th>Refunded</th><th>When</th></tr></thead>
              <tbody>
                ${this.usage.map(
                  (u) => html`<tr>
                    <td>${u.id}</td>
                    <td>${u.capability}</td>
                    <td>${u.assetId ?? u.liveStreamId ?? "—"}</td>
                    <td>${u.charge?.state ?? "—"}</td>
                    <td class="video-admin-page-money">${u.charge?.estimatedAmountCents !== null && u.charge?.estimatedAmountCents !== undefined ? `$${(u.charge.estimatedAmountCents / 100).toFixed(2)}` : "—"}</td>
                    <td class="video-admin-page-money">${u.charge?.committedAmountCents !== null && u.charge?.committedAmountCents !== undefined ? `$${(u.charge.committedAmountCents / 100).toFixed(2)}` : `$${(u.amountCents / 100).toFixed(2)}`}</td>
                    <td class="video-admin-page-money">${u.charge?.refundedAmountCents !== null && u.charge?.refundedAmountCents !== undefined ? `$${(u.charge.refundedAmountCents / 100).toFixed(2)}` : "—"}</td>
                    <td>${u.createdAt}</td>
                  </tr>`,
                )}
              </tbody>
            </table>
          </portal-detail-section>

          <portal-detail-section
            heading="Audit"
            description="Operator-visible customer history filtered to this account."
          >
            <table class="video-admin-page-table">
              <thead><tr><th>When</th><th>Actor</th><th>Action</th><th>Detail</th></tr></thead>
              <tbody>
                ${this.audit.map(
                  (a) => html`<tr>
                    <td>${a.ts}</td>
                    <td>${a.actor}</td>
                    <td>${a.action}</td>
                    <td>${a.detail}</td>
                  </tr>`,
                )}
              </tbody>
            </table>
          </portal-detail-section>
        </div>
      </portal-card>
      `,
      this,
    );
  }
}

function parseMaybeNumber(value: unknown): number | null {
  if (value === null || value === undefined) return null;
  const parsed = parseInt(String(value), 10);
  return Number.isFinite(parsed) ? parsed : null;
}

if (!customElements.get("admin-customer-detail")) {
  customElements.define("admin-customer-detail", AdminCustomerDetail);
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-customer-detail": AdminCustomerDetail;
  }
}
