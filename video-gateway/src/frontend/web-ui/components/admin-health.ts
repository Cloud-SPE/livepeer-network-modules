import { html, render, nothing } from "lit";
import { installAdminPageStyles } from "./admin-shared.js";

export class AdminHealth extends HTMLElement {
  private status: "loading" | "ok" | "err" = "loading";
  private message = "";

  async connectedCallback(): Promise<void> {
    installAdminPageStyles();
    this.draw();
    try {
      const res = await fetch("/healthz");
      this.status = res.ok ? "ok" : "err";
      this.message = await res.text();
    } catch (err) {
      this.status = "err";
      this.message = err instanceof Error ? err.message : "health_failed";
    }
    this.draw();
  }

  private draw(): void {
    render(
      html`
      <portal-card heading="Health">
        <portal-detail-section
          heading="Gateway health"
          description="Fast process-level readiness view for the video gateway."
        >
          ${this.status === "loading" ? html`<p>Loading.</p>` : nothing}
          ${this.status === "ok"
            ? html`<p class="video-admin-page-ok">Healthy: ${this.message.trim() || "ok"}</p>`
            : nothing}
          ${this.status === "err"
            ? html`<p class="video-admin-page-error">${this.message}</p>`
            : nothing}
        </portal-detail-section>
      </portal-card>
      `,
      this,
    );
  }
}

if (!customElements.get("admin-health")) {
  customElements.define("admin-health", AdminHealth);
}

declare global {
  interface HTMLElementTagNameMap {
    "admin-health": AdminHealth;
  }
}
