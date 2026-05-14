import { html, nothing, render } from "lit";
import { ApiClient } from "@livepeer-network-modules/customer-portal-shared";

import { installAdminPageStyles } from "./admin-shared.js";

interface UsageRow {
  id: string;
  projectId: string;
  assetId: string | null;
  liveStreamId: string | null;
  customerId: string | null;
  capability: string;
  amountCents: number;
  createdAt: string;
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

export class AdminUsage extends HTMLElement {
  private rows: UsageRow[] = [];
  private summary: UsageSummary | null = null;
  private error: string | null = null;
  private api = new ApiClient({ baseUrl: "" });

  async connectedCallback(): Promise<void> {
    installAdminPageStyles();
    this.draw();
    try {
      const out = await this.api.get<{
        items?: Array<Record<string, unknown>>;
        summary?: Record<string, unknown>;
      }>("/admin/usage?limit=100");
      this.rows = (out.items ?? []).map((row) => this.mapRow(row));
      this.summary = out.summary ? this.mapSummary(out.summary) : null;
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
    this.draw();
  }

  private mapRow(row: Record<string, unknown>): UsageRow {
    return {
      id: String(row["id"] ?? ""),
      projectId: String(row["project_id"] ?? ""),
      assetId: row["asset_id"] ? String(row["asset_id"]) : null,
      liveStreamId: row["live_stream_id"] ? String(row["live_stream_id"]) : null,
      customerId: row["charge"] && typeof row["charge"] === "object" && (row["charge"] as Record<string, unknown>)["customer_id"]
        ? String((row["charge"] as Record<string, unknown>)["customer_id"])
        : null,
      capability: String(row["capability"] ?? ""),
      amountCents: parseMaybeNumber(row["amount_cents"]) ?? 0,
      createdAt: String(row["created_at"] ?? ""),
      charge:
        row["charge"] && typeof row["charge"] === "object"
          ? {
              state: (row["charge"] as Record<string, unknown>)["state"]
                ? String((row["charge"] as Record<string, unknown>)["state"])
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

  private mapSummary(row: Record<string, unknown>): UsageSummary {
    return {
      topupTotalCents: parseMaybeNumber(row["topup_total_cents"]) ?? 0,
      usageCommittedCents: parseMaybeNumber(row["usage_committed_cents"]) ?? 0,
      reservedOpenCents: parseMaybeNumber(row["reserved_open_cents"]) ?? 0,
      refundedCents: parseMaybeNumber(row["refunded_cents"]) ?? 0,
    };
  }

  private draw(): void {
    render(
      html`
        <portal-card heading="Usage">
          <portal-data-table
            heading="Media billing ledger"
            description="Committed media usage joined to shared reservation state."
          >
            ${this.summary
              ? html`
                  <dl class="video-admin-page-meta-list">
                    <div class="video-admin-page-meta-item"><dt>Committed usage</dt><dd class="video-admin-page-money">$${(this.summary.usageCommittedCents / 100).toFixed(2)}</dd></div>
                    <div class="video-admin-page-meta-item"><dt>Open reservations</dt><dd class="video-admin-page-money">$${(this.summary.reservedOpenCents / 100).toFixed(2)}</dd></div>
                    <div class="video-admin-page-meta-item"><dt>Refunded reservations</dt><dd class="video-admin-page-money">$${(this.summary.refundedCents / 100).toFixed(2)}</dd></div>
                  </dl>
                `
              : nothing}
            ${this.error ? html`<p class="video-admin-page-error">${this.error}</p>` : nothing}
            <table class="video-admin-page-table">
              <thead>
                <tr><th>ID</th><th>Customer</th><th>Project</th><th>Capability</th><th>Target</th><th>State</th><th>Estimated</th><th>Committed</th><th>Refunded</th><th>When</th></tr>
              </thead>
              <tbody>
                ${this.rows.map(
                  (row) => html`<tr>
                    <td>${row.id}</td>
                    <td>${row.customerId ? html`<a class="video-admin-page-link" href="#/customers/${row.customerId}">${row.customerId}</a>` : "—"}</td>
                    <td>${row.projectId}</td>
                    <td>${row.capability}</td>
                    <td>${row.assetId ?? row.liveStreamId ?? "—"}</td>
                    <td>${row.charge?.state ?? "—"}</td>
                    <td class="video-admin-page-money">${row.charge?.estimatedAmountCents !== null && row.charge?.estimatedAmountCents !== undefined ? `$${(row.charge.estimatedAmountCents / 100).toFixed(2)}` : "—"}</td>
                    <td class="video-admin-page-money">${row.charge?.committedAmountCents !== null && row.charge?.committedAmountCents !== undefined ? `$${(row.charge.committedAmountCents / 100).toFixed(2)}` : `$${(row.amountCents / 100).toFixed(2)}`}</td>
                    <td class="video-admin-page-money">${row.charge?.refundedAmountCents !== null && row.charge?.refundedAmountCents !== undefined ? `$${(row.charge.refundedAmountCents / 100).toFixed(2)}` : "—"}</td>
                    <td>${row.createdAt}</td>
                  </tr>`,
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

if (!customElements.get("admin-usage")) {
  customElements.define("admin-usage", AdminUsage);
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-usage": AdminUsage;
  }
}

function parseMaybeNumber(value: unknown): number | null {
  if (value === null || value === undefined) return null;
  const parsed = parseInt(String(value), 10);
  return Number.isFinite(parsed) ? parsed : null;
}
