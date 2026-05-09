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

function installStyles(): void {
  if (document.getElementById("video-gateway-portal-pages-styles") !== null) {
    return;
  }
  const link = document.createElement("link");
  link.id = "video-gateway-portal-pages-styles";
  link.rel = "stylesheet";
  link.href = new URL("./portal-pages.css", import.meta.url).href;
  document.head.append(link);
}

export class PortalWebhooks extends HTMLElement {
  private rows: WebhookRow[] = [];
  private deliveries: DeliveryRow[] = [];
  private newUrl = "";
  private created: CreatedEndpoint | null = null;
  private secretRevealed = false;
  private error: string | null = null;

  private readonly api = new ApiClient({ baseUrl: "" });

  connectedCallback(): void {
    installStyles();
    void this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      const out = await this.api.get<{ items: WebhookRow[] }>("/portal/webhooks");
      this.rows = out.items ?? [];
      const deliveries = await this.api.get<{ items: DeliveryRow[] }>("/portal/webhook-deliveries");
      this.deliveries = deliveries.items ?? [];
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
    this.render();
  }

  private async createEndpoint(event: Event): Promise<void> {
    event.preventDefault();
    if (this.newUrl.trim() === "") {
      return;
    }
    try {
      const out = await this.api.post<CreatedEndpoint>("/portal/webhooks", {
        url: this.newUrl,
        events: ["asset.ready", "stream.started", "stream.ended", "recording.ready"],
      });
      this.created = out;
      this.secretRevealed = true;
      this.newUrl = "";
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "create_failed";
      this.render();
    }
  }

  private async rotate(row: WebhookRow): Promise<void> {
    if (!confirm(`Rotate signing secret for ${row.url}? Existing receivers must update.`)) {
      return;
    }
    try {
      const out = await this.api.post<CreatedEndpoint>(`/portal/webhooks/${encodeURIComponent(row.id)}/rotate`);
      this.created = out;
      this.secretRevealed = true;
      this.render();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "rotate_failed";
      this.render();
    }
  }

  private async deleteEndpoint(row: WebhookRow): Promise<void> {
    if (!confirm(`Remove webhook endpoint ${row.url}?`)) {
      return;
    }
    try {
      await this.api.request("DELETE", `/portal/webhooks/${encodeURIComponent(row.id)}`);
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "remove_failed";
      this.render();
    }
  }

  private async copySecret(): Promise<void> {
    if (this.created === null) {
      return;
    }
    try {
      await navigator.clipboard.writeText(this.created.signingSecret);
    } catch {
      // clipboard unavailable
    }
  }

  private render(): void {
    const card = document.createElement("portal-card");
    card.setAttribute("heading", "Webhooks");

    const shell = document.createElement("portal-data-table");
    shell.setAttribute("heading", "Endpoint Registry");
    shell.setAttribute("description", "Create webhook endpoints, rotate signing secrets, and monitor recent deliveries.");

    const form = document.createElement("form");
    form.className = "video-portal-page-form";
    form.slot = "toolbar";
    form.addEventListener("submit", (event) => {
      void this.createEndpoint(event);
    });
    const input = document.createElement("input");
    input.placeholder = "https://example.com/webhook";
    input.value = this.newUrl;
    input.addEventListener("input", (event) => {
      this.newUrl = (event.target as HTMLInputElement).value;
    });
    const addButton = document.createElement("portal-button");
    addButton.setAttribute("type", "submit");
    addButton.textContent = "Add endpoint";
    form.append(input, addButton);
    shell.append(form);

    if (this.created !== null) {
      const createdCard = document.createElement("portal-card");
      createdCard.setAttribute("heading", "Signing secret - copy now (one-time reveal)");
      createdCard.append(
        this.codeRow("URL", this.created.url),
        this.codeRow("Secret", this.secretRevealed ? this.created.signingSecret : "•••••••••••••"),
      );
      const actions = document.createElement("portal-action-row");
      const copy = document.createElement("portal-button");
      copy.setAttribute("variant", "ghost");
      copy.textContent = "Copy";
      copy.addEventListener("click", () => {
        void this.copySecret();
      });
      const dismiss = document.createElement("portal-button");
      dismiss.setAttribute("variant", "ghost");
      dismiss.textContent = "Dismiss";
      dismiss.addEventListener("click", () => {
        this.secretRevealed = false;
        this.created = null;
        this.render();
      });
      actions.append(copy, dismiss);
      createdCard.append(actions, this.message("p", "Stored hashed; never re-displayed. Rotate if leaked."));
      shell.append(createdCard);
    }

    if (this.error !== null) {
      shell.append(this.message("p", this.error, "video-portal-page-error"));
    }

    const endpointsTable = document.createElement("table");
    endpointsTable.className = "video-portal-page-table";
    endpointsTable.innerHTML = `
      <thead>
        <tr><th>URL</th><th>Events</th><th>Last delivery</th><th>Created</th><th></th></tr>
      </thead>
      <tbody></tbody>
    `;
    const endpointsBody = endpointsTable.tBodies[0]!;
    for (const row of this.rows) {
      const tr = document.createElement("tr");
      tr.append(
        this.codeCell(row.url),
        this.cell(row.events.join(", ")),
        this.deliveryCell(row),
        this.cell(row.createdAt),
        this.endpointActions(row),
      );
      endpointsBody.append(tr);
    }
    shell.append(endpointsTable);
    card.append(shell, this.deliveryLog(), this.hmacExample());
    this.replaceChildren(card);
  }

  private deliveryLog(): HTMLElement {
    const section = document.createElement("section");
    const shell = document.createElement("portal-data-table");
    shell.setAttribute("heading", "Delivery Log");
    shell.setAttribute("description", "Recent webhook deliveries with status codes and retry counts.");
    const table = document.createElement("table");
    table.className = "video-portal-page-table";
    table.innerHTML = `
      <thead>
        <tr><th>Event</th><th>Endpoint</th><th>Status</th><th>Attempts</th><th>When</th></tr>
      </thead>
      <tbody></tbody>
    `;
    const tbody = table.tBodies[0]!;
    for (const delivery of this.deliveries) {
      const tr = document.createElement("tr");
      tr.append(
        this.cell(delivery.eventType),
        this.cell(delivery.endpointId),
        this.statusCell(
          delivery.statusCode === null ? "pending" : String(delivery.statusCode),
          delivery.statusCode === null ? "neutral" : delivery.statusCode >= 500 ? "danger" : delivery.statusCode >= 400 ? "warning" : "success",
        ),
        this.cell(String(delivery.attempts)),
        this.deliveryTimeCell(delivery.deliveredAt),
      );
      tbody.append(tr);
    }
    shell.append(table);
    section.append(shell);
    return section;
  }

  private hmacExample(): HTMLElement {
    const section = document.createElement("section");
    const heading = document.createElement("h3");
    heading.textContent = "HMAC verification example";
    const pre = document.createElement("pre");
    pre.className = "video-portal-page-pre";
    pre.textContent = HMAC_SNIPPET;
    section.append(heading, pre);
    return section;
  }

  private message<K extends keyof HTMLElementTagNameMap>(tag: K, text: string, className = ""): HTMLElementTagNameMap[K] {
    const element = document.createElement(tag);
    if (className !== "") {
      element.className = className;
    }
    element.textContent = text;
    return element;
  }

  private cell(text: string): HTMLTableCellElement {
    const td = document.createElement("td");
    td.textContent = text;
    return td;
  }

  private codeCell(text: string): HTMLTableCellElement {
    const td = document.createElement("td");
    const code = document.createElement("code");
    code.textContent = text;
    td.append(code);
    return td;
  }

  private statusCell(text: string, variant: string): HTMLTableCellElement {
    const td = document.createElement("td");
    const pill = document.createElement("portal-status-pill");
    pill.setAttribute("variant", variant);
    pill.textContent = text;
    td.append(pill);
    return td;
  }

  private deliveryCell(row: WebhookRow): HTMLTableCellElement {
    const td = document.createElement("td");
    if (row.lastDeliveryStatus === null) {
      td.append(this.message("span", "-", "video-portal-page-dim"));
      return td;
    }
    const pill = document.createElement("portal-status-pill");
    pill.setAttribute(
      "variant",
      row.lastDeliveryStatus >= 500 ? "danger" : row.lastDeliveryStatus >= 400 ? "warning" : "success",
    );
    pill.textContent = String(row.lastDeliveryStatus);
    td.append(pill, this.message("span", ` at ${row.lastDeliveryAt ?? ""}`, "video-portal-page-dim"));
    return td;
  }

  private deliveryTimeCell(deliveredAt: string | null): HTMLTableCellElement {
    const td = document.createElement("td");
    if (deliveredAt === null) {
      td.append(this.message("span", "queued", "video-portal-page-dim"));
      return td;
    }
    td.textContent = deliveredAt;
    return td;
  }

  private endpointActions(row: WebhookRow): HTMLTableCellElement {
    const td = document.createElement("td");
    const actions = document.createElement("portal-action-row");
    actions.setAttribute("align", "end");
    const rotate = document.createElement("portal-button");
    rotate.setAttribute("variant", "ghost");
    rotate.textContent = "Rotate";
    rotate.addEventListener("click", () => {
      void this.rotate(row);
    });
    const remove = document.createElement("portal-button");
    remove.setAttribute("variant", "danger");
    remove.textContent = "Remove";
    remove.addEventListener("click", () => {
      void this.deleteEndpoint(row);
    });
    actions.append(rotate, remove);
    td.append(actions);
    return td;
  }

  private codeRow(label: string, value: string): HTMLElement {
    const p = document.createElement("p");
    const strong = document.createElement("strong");
    strong.textContent = `${label}: `;
    const code = document.createElement("code");
    code.textContent = value;
    p.append(strong, code);
    return p;
  }
}

if (!customElements.get("portal-webhooks")) {
  customElements.define("portal-webhooks", PortalWebhooks);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-webhooks": PortalWebhooks;
  }
}
