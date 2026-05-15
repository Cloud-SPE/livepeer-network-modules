// Waitlist queue. Approve creates a customer + issues an API key (the
// plaintext is shown ONCE, after which the admin is responsible for
// passing it to the user out-of-band). Reject is a soft tombstone —
// the row stays for audit but doesn't create anything in customer-portal.

import {
  DaydreamAdminApi,
  type WaitlistEntry,
} from "../lib/api.js";

export class AdminDaydreamSignups extends HTMLElement {
  private api = new DaydreamAdminApi();
  private entries: WaitlistEntry[] = [];
  private filter: WaitlistEntry["status"] | "all" = "pending";
  private loading = true;
  private error: string | null = null;
  private justIssued: { email: string; apiKey: string } | null = null;

  connectedCallback(): void {
    this.render();
    void this.load();
  }

  private async load(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await this.api.listWaitlist(
        this.filter === "all" ? undefined : this.filter,
      );
      this.entries = res.entries;
    } catch (err) {
      this.error = `Could not load waitlist: ${err instanceof Error ? err.message : String(err)}`;
    } finally {
      this.loading = false;
      this.render();
    }
  }

  private render(): void {
    if (this.justIssued) {
      this.innerHTML = `
        <portal-card heading="Approved">
          <p>Customer created for <strong>${escapeHtml(this.justIssued.email)}</strong>.</p>
          <p>Their API key — <strong>shown once</strong>. Copy it now
          and share out-of-band:</p>
          <pre style="background:#f6f6f6;padding:.5rem;border-radius:4px;word-break:break-all">${escapeHtml(this.justIssued.apiKey)}</pre>
          <portal-action-row>
            <portal-button id="dd-issued-copy">Copy</portal-button>
            <portal-button id="dd-issued-done" variant="ghost">Back to queue</portal-button>
          </portal-action-row>
        </portal-card>`;
      this.querySelector("#dd-issued-copy")?.addEventListener("click", () => {
        if (this.justIssued) {
          void navigator.clipboard.writeText(this.justIssued.apiKey);
        }
      });
      this.querySelector("#dd-issued-done")?.addEventListener("click", () => {
        this.justIssued = null;
        void this.load();
      });
      return;
    }

    this.innerHTML = `
      <portal-card heading="Waitlist">
        <portal-action-row>
          ${this.renderFilter("pending")}
          ${this.renderFilter("approved")}
          ${this.renderFilter("rejected")}
          ${this.renderFilter("all")}
        </portal-action-row>
        ${this.loading ? "<p>Loading…</p>" : ""}
        ${this.error ? `<p class="portal-form-error">${escapeHtml(this.error)}</p>` : ""}
        <table style="width:100%;border-collapse:collapse;font-size:.9em;margin-top:.5rem">
          <thead>
            <tr>
              <th style="text-align:left;padding:.25rem">Email</th>
              <th style="text-align:left;padding:.25rem">Display name</th>
              <th style="text-align:left;padding:.25rem">Status</th>
              <th style="text-align:left;padding:.25rem">Requested</th>
              <th style="text-align:right;padding:.25rem">Action</th>
            </tr>
          </thead>
          <tbody>
            ${this.entries.map((e) => this.renderRow(e)).join("")}
          </tbody>
        </table>
        ${this.entries.length === 0 && !this.loading ? "<p>No entries.</p>" : ""}
      </portal-card>`;

    this.entries.forEach((e) => {
      this.querySelector(`[data-action="approve"][data-id="${e.id}"]`)
        ?.addEventListener("click", () => void this.approve(e));
      this.querySelector(`[data-action="reject"][data-id="${e.id}"]`)
        ?.addEventListener("click", () => void this.reject(e));
    });
    this.querySelectorAll<HTMLElement>("[data-filter]").forEach((el) => {
      el.addEventListener("click", () => {
        const f = el.dataset.filter as WaitlistEntry["status"] | "all";
        this.filter = f;
        void this.load();
      });
    });
  }

  private renderFilter(status: WaitlistEntry["status"] | "all"): string {
    const variant = this.filter === status ? "" : 'variant="ghost"';
    return `<portal-button data-filter="${status}" ${variant}>${status}</portal-button>`;
  }

  private renderRow(e: WaitlistEntry): string {
    const actions =
      e.status === "pending"
        ? `<portal-button data-action="approve" data-id="${e.id}">Approve</portal-button>
           <portal-button data-action="reject" data-id="${e.id}" variant="danger">Reject</portal-button>`
        : "—";
    return `
      <tr style="border-top:1px solid var(--portal-border,#eee)">
        <td style="padding:.25rem">${escapeHtml(e.email)}</td>
        <td style="padding:.25rem">${escapeHtml(e.display_name ?? "—")}</td>
        <td style="padding:.25rem"><portal-status-pill status="${e.status}"></portal-status-pill></td>
        <td style="padding:.25rem">${escapeHtml(new Date(e.created_at).toLocaleString())}</td>
        <td style="padding:.25rem;text-align:right">${actions}</td>
      </tr>`;
  }

  private async approve(e: WaitlistEntry): Promise<void> {
    if (!confirm(`Approve ${e.email}? An API key will be issued.`)) return;
    try {
      const res = await this.api.approveWaitlist(e.id);
      this.justIssued = { email: e.email, apiKey: res.api_key };
      this.render();
    } catch (err) {
      this.error = `Approve failed: ${err instanceof Error ? err.message : String(err)}`;
      this.render();
    }
  }

  private async reject(e: WaitlistEntry): Promise<void> {
    const reason = prompt("Rejection reason (optional)") ?? "";
    try {
      await this.api.rejectWaitlist(e.id, reason || undefined);
      await this.load();
    } catch (err) {
      this.error = `Reject failed: ${err instanceof Error ? err.message : String(err)}`;
      this.render();
    }
  }
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

customElements.define("admin-daydream-signups", AdminDaydreamSignups);
