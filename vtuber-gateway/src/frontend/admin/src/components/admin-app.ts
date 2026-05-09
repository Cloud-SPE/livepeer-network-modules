import { ApiClient, HashRouter, clearSession, readSession, writeSession } from "@livepeer-rewrite/customer-portal-shared";

export const VTUBER_GATEWAY_ADMIN_APP_TAG = "vtuber-gateway-admin";

type RouteKey = "health" | "customers" | "sessions" | "usage" | "nodes" | "rate-card" | "audit";

interface RouteState {
  view: RouteKey;
}

interface CustomerRow {
  id: string;
  email: string;
  tier: string;
  status: string;
  balanceCents: number;
  reservedCents: number;
}

interface SessionRow {
  id: string;
  customerId: string;
  status: string;
  persona: string | null;
  llmProvider: string | null;
  ttsProvider: string | null;
  nodeId: string | null;
  payerWorkId: string | null;
  createdAt: string;
  expiresAt: string;
  endedAt: string | null;
  errorCode: string | null;
}

interface UsageRow {
  id: string;
  sessionId: string;
  customerId: string;
  seconds: number;
  cents: number;
  createdAt: string;
}

interface NodeRow {
  nodeId: string;
  nodeUrl: string;
  lastSuccessAt: string | null;
  lastFailureAt: string | null;
  consecutiveFails: number;
  circuitOpen: boolean;
  updatedAt: string;
}

interface RateCardRow {
  offering: string;
  usdPerSecond: string;
  createdAt: string | null;
  updatedAt: string | null;
}

interface AuditRow {
  ts: string;
  actor: string;
  action: string;
  detail: string;
}

function installStyles(): void {
  if (document.getElementById("vtuber-gateway-admin-styles") !== null) {
    return;
  }
  const link = document.createElement("link");
  link.id = "vtuber-gateway-admin-styles";
  link.rel = "stylesheet";
  link.href = new URL("./admin-app.css", import.meta.url).href;
  document.head.append(link);
}

export class VtuberGatewayAdmin extends HTMLElement {
  private route: RouteState = { view: "health" };
  private authed = !!readSession()?.token;
  private error: string | null = null;
  private customers: CustomerRow[] = [];
  private sessions: SessionRow[] = [];
  private usage: UsageRow[] = [];
  private nodes: NodeRow[] = [];
  private rateCard: RateCardRow[] = [];
  private audit: AuditRow[] = [];

  private readonly api = new ApiClient({ baseUrl: "" });
  private router: HashRouter | null = null;

  connectedCallback(): void {
    installStyles();
    this.router = new HashRouter();
    this.router
      .add("/health", () => this.setRoute("health"))
      .add("/customers", () => this.setRoute("customers"))
      .add("/sessions", () => this.setRoute("sessions"))
      .add("/usage", () => this.setRoute("usage"))
      .add("/nodes", () => this.setRoute("nodes"))
      .add("/rate-card", () => this.setRoute("rate-card"))
      .add("/audit", () => this.setRoute("audit"));
    this.router.start();
    window.addEventListener("storage", this.onSessionChange);
    this.render();
    if (this.authed) {
      void this.refresh();
    }
  }

  disconnectedCallback(): void {
    window.removeEventListener("storage", this.onSessionChange);
  }

  private setRoute(view: RouteKey): void {
    this.route = { view };
    this.render();
  }

  private render(): void {
    this.replaceChildren(this.renderShell());
  }

  private renderShell(): HTMLElement {
    const layout = document.createElement("portal-layout");
    layout.setAttribute("brand", "Livepeer VTuber Admin");

    const nav = document.createElement("nav");
    nav.slot = "nav";
    nav.className = "vtuber-admin-nav";
    nav.setAttribute("aria-label", "Primary");

    if (this.authed) {
      nav.append(
        this.renderNavLink("/health", "Health", "health"),
        this.renderNavLink("/customers", "Customers", "customers"),
        this.renderNavLink("/sessions", "Sessions", "sessions"),
        this.renderNavLink("/usage", "Usage", "usage"),
        this.renderNavLink("/nodes", "Nodes", "nodes"),
        this.renderNavLink("/rate-card", "Rate Card", "rate-card"),
        this.renderNavLink("/audit", "Audit", "audit"),
        this.renderSignOutLink(),
      );
    }

    const hero = document.createElement("section");
    hero.className = "vtuber-admin-hero";
    hero.append(
      this.renderText("span", "vtuber-admin-eyebrow", "VTuber Gateway"),
      this.renderText("h1", "vtuber-admin-title", "Operator console for persona sessions, usage, and route health."),
      this.renderText(
        "p",
        "vtuber-admin-lede",
        "This surface pairs the shared customer ledger with VTuber-specific runtime views: live sessions, per-session usage, worker health, and current rate-card state.",
      ),
    );

    const content = document.createElement("section");
    content.className = "vtuber-admin-content";
    content.append(this.authed ? this.renderView() : this.renderLogin());
    if (this.error !== null) {
      const toast = document.createElement("portal-toast");
      toast.setAttribute("variant", "danger");
      toast.setAttribute("message", this.error);
      content.append(toast);
    }

    const footer = document.createElement("span");
    footer.slot = "footer";
    footer.textContent = "Livepeer VTuber operator console";

    layout.append(nav, hero, content, footer);
    return layout;
  }

  private renderNavLink(to: string, label: string, key: RouteKey): HTMLAnchorElement {
    const link = document.createElement("a");
    link.href = `#${to}`;
    link.textContent = label;
    if (this.route.view === key) {
      link.className = "active";
    }
    return link;
  }

  private renderSignOutLink(): HTMLAnchorElement {
    const link = document.createElement("a");
    link.href = "#/health";
    link.textContent = "Sign out";
    link.addEventListener("click", (event) => {
      event.preventDefault();
      this.signOut();
    });
    return link;
  }

  private renderText<K extends keyof HTMLElementTagNameMap>(
    tag: K,
    className: string,
    text: string,
  ): HTMLElementTagNameMap[K] {
    const element = document.createElement(tag);
    element.className = className;
    element.textContent = text;
    return element;
  }

  private renderLogin(): HTMLElement {
    const card = document.createElement("portal-card");
    card.setAttribute("heading", "Admin sign in");

    const form = document.createElement("form");
    form.className = "vtuber-admin-login-form";
    form.addEventListener("submit", (event) => {
      void this.onAdminLogin(event);
    });

    const tokenInput = document.createElement("portal-input");
    tokenInput.setAttribute("name", "token");
    tokenInput.setAttribute("label", "Admin token");
    tokenInput.setAttribute("required", "");

    const actorInput = document.createElement("portal-input");
    actorInput.setAttribute("name", "actor");
    actorInput.setAttribute("label", "Actor");
    actorInput.setAttribute("required", "");

    const button = document.createElement("portal-button");
    button.setAttribute("type", "submit");
    button.textContent = "Sign in";

    form.append(tokenInput, actorInput, button);
    card.append(form);
    return card;
  }

  private renderView(): HTMLElement {
    switch (this.route.view) {
      case "customers":
        return this.renderTableCard(
          "Customers",
          ["ID", "Email", "Tier", "Status", "Balance", "Reserved"],
          this.customers.map((row) => [
            row.id,
            row.email,
            row.tier,
            row.status,
            this.moneyCell(row.balanceCents),
            this.moneyCell(row.reservedCents),
          ]),
        );
      case "sessions":
        return this.renderTableCard(
          "Sessions",
          ["ID", "Customer", "Status", "Persona", "LLM", "TTS", "Node", "Created"],
          this.sessions.map((row) => [
            row.id,
            row.customerId,
            row.status,
            row.persona ?? "—",
            row.llmProvider ?? "—",
            row.ttsProvider ?? "—",
            row.nodeId ?? "—",
            row.createdAt,
          ]),
        );
      case "usage":
        return this.renderTableCard(
          "Usage",
          ["ID", "Session", "Customer", "Seconds", "Cents", "Created"],
          this.usage.map((row) => [
            row.id,
            row.sessionId,
            row.customerId,
            String(row.seconds),
            this.moneyCell(row.cents),
            row.createdAt,
          ]),
        );
      case "nodes":
        return this.renderTableCard(
          "Nodes",
          ["Node", "URL", "Fails", "Circuit", "Last success", "Last failure"],
          this.nodes.map((row) => [
            row.nodeId,
            row.nodeUrl,
            String(row.consecutiveFails),
            row.circuitOpen ? "open" : "closed",
            row.lastSuccessAt ?? "—",
            row.lastFailureAt ?? "—",
          ]),
        );
      case "rate-card":
        return this.renderTableCard(
          "Rate card",
          ["Offering", "USD / second", "Updated"],
          this.rateCard.map((row) => [
            row.offering,
            row.usdPerSecond,
            row.updatedAt ?? "config default",
          ]),
        );
      case "audit":
        return this.renderTableCard(
          "Audit",
          ["When", "Actor", "Action", "Detail"],
          this.audit.map((row) => [row.ts, row.actor, row.action, row.detail]),
        );
      case "health":
      default:
        return this.renderHealth();
    }
  }

  private renderHealth(): HTMLElement {
    const shell = document.createElement("div");
    shell.className = "vtuber-admin-health-grid";
    shell.append(
      this.renderMetricTile("Customers", String(this.customers.length)),
      this.renderMetricTile("Sessions", String(this.sessions.length)),
      this.renderMetricTile("Usage rows", String(this.usage.length)),
      this.renderMetricTile("Nodes tracked", String(this.nodes.length)),
    );
    return shell;
  }

  private renderMetricTile(label: string, value: string): HTMLElement {
    const tile = document.createElement("portal-metric-tile");
    tile.setAttribute("label", label);
    tile.setAttribute("value", value);
    return tile;
  }

  private renderTableCard(heading: string, headers: string[], rows: Array<Array<string | HTMLElement>>): HTMLElement {
    const card = document.createElement("portal-card");
    card.setAttribute("heading", heading);

    const table = document.createElement("table");
    table.className = "vtuber-admin-table";

    const thead = document.createElement("thead");
    const headRow = document.createElement("tr");
    for (const header of headers) {
      const th = document.createElement("th");
      th.textContent = header;
      headRow.append(th);
    }
    thead.append(headRow);

    const tbody = document.createElement("tbody");
    for (const row of rows) {
      const tr = document.createElement("tr");
      for (const value of row) {
        const td = document.createElement("td");
        if (typeof value === "string") {
          td.textContent = value;
        } else {
          td.append(value);
        }
        tr.append(td);
      }
      tbody.append(tr);
    }

    table.append(thead, tbody);
    card.append(table);
    return card;
  }

  private moneyCell(cents: number): HTMLElement {
    const span = document.createElement("span");
    span.className = "vtuber-admin-money";
    span.textContent = `$${(cents / 100).toFixed(2)}`;
    return span;
  }

  private async onAdminLogin(event: Event): Promise<void> {
    event.preventDefault();
    const form = new FormData(event.currentTarget as HTMLFormElement);
    writeSession({
      token: String(form.get("token") ?? ""),
      actor: String(form.get("actor") ?? ""),
    });
    this.authed = !!readSession()?.token;
    this.render();
    await this.refresh();
    window.location.hash = "#/health";
  }

  private readonly onSessionChange = (): void => {
    this.authed = !!readSession()?.token;
    this.render();
  };

  private signOut(): void {
    clearSession();
    this.authed = false;
    this.render();
    window.location.hash = "#/health";
  }

  private async refresh(): Promise<void> {
    this.error = null;
    try {
      const [customersOut, sessionsOut, usageOut, nodesOut, rateCardOut, auditOut] = await Promise.all([
        this.api.get<{ customers?: Array<Record<string, unknown>> }>("/admin/customers?limit=50"),
        this.api.get<{ sessions?: Array<Record<string, unknown>> }>("/admin/vtuber/sessions"),
        this.api.get<{ usage?: Array<Record<string, unknown>> }>("/admin/vtuber/usage"),
        this.api.get<{ nodes?: Array<Record<string, unknown>> }>("/admin/vtuber/node-health"),
        this.api.get<{ rate_card?: Array<Record<string, unknown>> }>("/admin/vtuber/rate-card"),
        this.api.get<{ events?: Array<Record<string, unknown>> }>("/admin/audit?limit=50"),
      ]);
      this.customers = (customersOut.customers ?? []).map((row: Record<string, unknown>) => ({
        id: String(row["id"] ?? ""),
        email: String(row["email"] ?? ""),
        tier: String(row["tier"] ?? ""),
        status: String(row["status"] ?? ""),
        balanceCents: parseInt(String(row["balance_usd_cents"] ?? "0"), 10) || 0,
        reservedCents: parseInt(String(row["reserved_usd_cents"] ?? "0"), 10) || 0,
      }));
      this.sessions = (sessionsOut.sessions ?? []).map((row: Record<string, unknown>) => ({
        id: String(row["id"] ?? ""),
        customerId: String(row["customer_id"] ?? ""),
        status: String(row["status"] ?? ""),
        persona: row["persona"] ? String(row["persona"]) : null,
        llmProvider: row["llm_provider"] ? String(row["llm_provider"]) : null,
        ttsProvider: row["tts_provider"] ? String(row["tts_provider"]) : null,
        nodeId: row["node_id"] ? String(row["node_id"]) : null,
        payerWorkId: row["payer_work_id"] ? String(row["payer_work_id"]) : null,
        createdAt: String(row["created_at"] ?? ""),
        expiresAt: String(row["expires_at"] ?? ""),
        endedAt: row["ended_at"] ? String(row["ended_at"]) : null,
        errorCode: row["error_code"] ? String(row["error_code"]) : null,
      }));
      this.usage = (usageOut.usage ?? []).map((row: Record<string, unknown>) => ({
        id: String(row["id"] ?? ""),
        sessionId: String(row["session_id"] ?? ""),
        customerId: String(row["customer_id"] ?? ""),
        seconds: Number(row["seconds"] ?? 0),
        cents: parseInt(String(row["cents"] ?? "0"), 10) || 0,
        createdAt: String(row["created_at"] ?? ""),
      }));
      this.nodes = (nodesOut.nodes ?? []).map((row: Record<string, unknown>) => ({
        nodeId: String(row["node_id"] ?? ""),
        nodeUrl: String(row["node_url"] ?? ""),
        lastSuccessAt: row["last_success_at"] ? String(row["last_success_at"]) : null,
        lastFailureAt: row["last_failure_at"] ? String(row["last_failure_at"]) : null,
        consecutiveFails: Number(row["consecutive_fails"] ?? 0),
        circuitOpen: Boolean(row["circuit_open"]),
        updatedAt: String(row["updated_at"] ?? ""),
      }));
      this.rateCard = (rateCardOut.rate_card ?? []).map((row: Record<string, unknown>) => ({
        offering: String(row["offering"] ?? ""),
        usdPerSecond: String(row["usd_per_second"] ?? ""),
        createdAt: row["created_at"] ? String(row["created_at"]) : null,
        updatedAt: row["updated_at"] ? String(row["updated_at"]) : null,
      }));
      this.audit = (auditOut.events ?? []).map((row: Record<string, unknown>) => ({
        ts: String(row["ts"] ?? ""),
        actor: String(row["actor"] ?? ""),
        action: String(row["action"] ?? ""),
        detail: String(row["detail"] ?? ""),
      }));
    } catch (err) {
      this.error = err instanceof Error ? err.message : "refresh_failed";
    }
    this.render();
  }
}

if (!customElements.get("vtuber-gateway-admin")) {
  customElements.define("vtuber-gateway-admin", VtuberGatewayAdmin);
}

declare global {
  interface HTMLElementTagNameMap {
    "vtuber-gateway-admin": VtuberGatewayAdmin;
  }
}
