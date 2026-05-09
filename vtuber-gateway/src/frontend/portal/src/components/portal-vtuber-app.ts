import { ApiClient, HashRouter, clearSession, readSession } from "@livepeer-rewrite/customer-portal-shared";

type RouteView = "signup" | "login" | "account" | "api-keys" | "billing" | "sessions" | "persona" | "history";

interface RouteState {
  view: RouteView;
  params: Record<string, string>;
}

interface PortalCustomer {
  id: string;
  email: string;
  tier: string;
  status: string;
  balance_usd_cents: string;
  reserved_usd_cents: string;
}

interface PortalLimits {
  balance_usd_cents: string;
  reserved_usd_cents: string;
  quota_tokens_remaining: string | null;
  quota_monthly_allowance: string | null;
  quota_reserved_tokens: string;
  quota_reset_at: string | null;
}

interface CredentialSummary {
  id: string;
  label: string | null;
  created_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
}

interface TopupSummary {
  id: string;
  amount_usd_cents: string;
  status: string;
  created_at: string;
}

interface PortalApiKeysElement extends HTMLElement {
  keys: readonly Record<string, string | null>[];
  showPlaintext(value: string): void;
}

function installStyles(): void {
  if (document.getElementById("vtuber-gateway-portal-styles") !== null) {
    return;
  }
  const link = document.createElement("link");
  link.id = "vtuber-gateway-portal-styles";
  link.rel = "stylesheet";
  link.href = new URL("./portal-vtuber-app.css", import.meta.url).href;
  document.head.append(link);
}

export class VtuberGatewayPortal extends HTMLElement {
  private route: RouteState = { view: "sessions", params: {} };
  private authed = !!readSession()?.token;
  private customer: PortalCustomer | null = null;
  private limits: PortalLimits | null = null;
  private authTokens: CredentialSummary[] = [];
  private apiKeys: CredentialSummary[] = [];
  private topups: TopupSummary[] = [];
  private error: string | null = null;

  private readonly api = new ApiClient({ baseUrl: "" });
  private router: HashRouter | null = null;

  connectedCallback(): void {
    installStyles();
    this.router = new HashRouter();
    this.router
      .add("/signup", () => this.setRoute("signup"))
      .add("/login", () => this.setRoute("login"))
      .add("/account", () => this.setRoute("account"))
      .add("/api-keys", () => this.setRoute("api-keys"))
      .add("/billing", () => this.setRoute("billing"))
      .add("/sessions", () => this.setRoute("sessions"))
      .add("/persona", () => this.setRoute("persona"))
      .add("/history", () => this.setRoute("history"));
    this.router.start();
    window.addEventListener("storage", this.onSessionChange);
    this.render();
    if (this.authed) {
      void this.loadPortalState();
    }
  }

  disconnectedCallback(): void {
    window.removeEventListener("storage", this.onSessionChange);
  }

  private setRoute(view: RouteView): void {
    this.route = { view, params: {} };
    this.render();
  }

  private render(): void {
    this.replaceChildren(this.renderShell());
  }

  private renderShell(): HTMLElement {
    const layout = document.createElement("portal-layout");
    layout.setAttribute("brand", "Livepeer VTuber");

    const nav = document.createElement("nav");
    nav.slot = "nav";
    nav.className = "vtuber-portal-nav";
    nav.setAttribute("aria-label", "Primary");
    if (!this.authed) {
      nav.append(
        this.renderNavLink("/login", "Sign in", "login"),
        this.renderNavLink("/signup", "Create account", "signup"),
      );
    }
    nav.append(
      this.renderNavLink("/account", "Account", "account"),
      this.renderNavLink("/api-keys", "API keys", "api-keys"),
      this.renderNavLink("/billing", "Billing", "billing"),
      this.renderNavLink("/sessions", "Sessions", "sessions"),
      this.renderNavLink("/persona", "Persona", "persona"),
      this.renderNavLink("/history", "History", "history"),
    );
    if (this.authed) {
      nav.append(this.renderSignOutLink());
    }

    const hero = document.createElement("section");
    hero.className = "vtuber-portal-hero";
    hero.append(
      this.renderText("span", "vtuber-portal-eyebrow", "VTuber Portal"),
      this.renderText(
        "h1",
        "vtuber-portal-title",
        "Operate persona-driven realtime sessions with the same network language as the rest of Livepeer.",
      ),
      this.renderText(
        "p",
        "vtuber-portal-lede",
        "This surface is the expressive edge of the control plane: session orchestration, persona tuning, and scene continuity, wrapped in the same visual system as the OpenAI and video gateways.",
      ),
      this.renderFeatureGrid(),
    );

    const content = document.createElement("section");
    content.className = "vtuber-portal-content";
    if (this.error !== null) {
      const toast = document.createElement("portal-toast");
      toast.setAttribute("variant", "danger");
      toast.setAttribute("message", this.error);
      content.append(toast);
    }
    content.append(this.renderView());

    const footer = document.createElement("span");
    footer.slot = "footer";
    footer.textContent = "Livepeer VTuber customer portal";

    layout.append(nav, hero, content, footer);
    return layout;
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

  private renderNavLink(to: string, label: string, key: RouteView): HTMLAnchorElement {
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
    link.href = "#/login";
    link.textContent = "Sign out";
    link.addEventListener("click", (event) => {
      event.preventDefault();
      this.signOut();
    });
    return link;
  }

  private renderFeatureGrid(): HTMLElement {
    const grid = document.createElement("div");
    grid.className = "vtuber-portal-feature-grid";
    grid.append(
      this.renderMetricTile("Mode", "Session Control"),
      this.renderMetricTile("Media Shape", "Realtime VTuber"),
      this.renderMetricTile("Billing Model", "Session + Top-up"),
    );
    return grid;
  }

  private renderMetricTile(label: string, value: string): HTMLElement {
    const tile = document.createElement("portal-metric-tile");
    tile.setAttribute("label", label);
    tile.setAttribute("value", value);
    return tile;
  }

  private renderView(): HTMLElement {
    if (!this.authed && this.route.view !== "signup" && this.route.view !== "login") {
      return this.wrapCard("Sign in", null, this.renderLogin());
    }
    switch (this.route.view) {
      case "signup":
        return this.wrapCard("Create account", null, this.renderSignup());
      case "login":
        return this.wrapCard("Sign in", null, this.renderLogin());
      case "account":
        return this.accountView();
      case "api-keys":
        return this.credentialsView();
      case "billing":
        return this.billingView();
      case "sessions": {
        const el = document.createElement("portal-vtuber-sessions");
        return el;
      }
      case "persona": {
        const el = document.createElement("portal-vtuber-persona");
        return el;
      }
      case "history": {
        const el = document.createElement("portal-vtuber-history");
        return el;
      }
      default: {
        const el = document.createElement("portal-vtuber-sessions");
        return el;
      }
    }
  }

  private wrapCard(heading: string, subheading: string | null, child: HTMLElement): HTMLElement {
    const card = document.createElement("portal-card");
    card.setAttribute("heading", heading);
    if (subheading !== null) {
      card.setAttribute("subheading", subheading);
    }
    card.append(child);
    return card;
  }

  private renderLogin(): HTMLElement {
    const login = document.createElement("portal-login");
    login.addEventListener("portal-login-success", () => this.onSignedIn());
    return login;
  }

  private renderSignup(): HTMLElement {
    const signup = document.createElement("portal-signup");
    signup.addEventListener("portal-signup-success", () => this.onSignedIn());
    return signup;
  }

  private onSignedIn(): void {
    this.authed = !!readSession()?.token;
    this.render();
    void this.loadPortalState();
    window.location.hash = "#/account";
  }

  private readonly onSessionChange = (): void => {
    this.authed = !!readSession()?.token;
    if (this.authed) {
      void this.loadPortalState();
    } else {
      this.customer = null;
      this.limits = null;
      this.authTokens = [];
      this.apiKeys = [];
      this.topups = [];
      this.render();
    }
  };

  private signOut(): void {
    clearSession();
    this.authed = false;
    this.customer = null;
    this.limits = null;
    this.authTokens = [];
    this.apiKeys = [];
    this.topups = [];
    this.render();
    window.location.hash = "#/login";
  }

  private accountView(): HTMLElement {
    const balance = Number(this.customer?.balance_usd_cents ?? this.limits?.balance_usd_cents ?? "0");
    const reserved = Number(this.customer?.reserved_usd_cents ?? this.limits?.reserved_usd_cents ?? "0");

    const card = document.createElement("portal-card");
    card.setAttribute("heading", "Account");
    card.setAttribute("subheading", "Portal identity, current balance, and quota guardrails.");

    const balanceWidget = document.createElement("portal-balance") as HTMLElement & {
      balanceCents: number;
      reservedCents: number;
    };
    balanceWidget.balanceCents = balance;
    balanceWidget.reservedCents = reserved;

    card.append(
      balanceWidget,
      this.metaList([
        ["Email", this.customer?.email ?? "—"],
        ["Tier", this.customer?.tier ?? "—"],
        ["Status", this.customer?.status ?? "—"],
        ["Quota remaining", this.limits?.quota_tokens_remaining ?? "—"],
        ["Monthly allowance", this.limits?.quota_monthly_allowance ?? "—"],
        ["Reserved quota", this.limits?.quota_reserved_tokens ?? "—"],
        ["Quota reset", this.limits?.quota_reset_at ?? "—"],
      ]),
    );

    return card;
  }

  private credentialsView(): HTMLElement {
    const card = document.createElement("portal-card");
    card.setAttribute("heading", "Credentials");
    card.setAttribute("subheading", "Manage monorepo UI auth tokens separately from VTuber product API keys.");

    const authSection = document.createElement("portal-detail-section");
    authSection.setAttribute("heading", "UI auth tokens");
    authSection.setAttribute("description", "Used to sign into the VTuber portal and other shared browser surfaces.");
    const authKeys = document.createElement("portal-api-keys") as unknown as PortalApiKeysElement;
    authKeys.keys = (this.authTokens ?? []).map((item) => this.toWidgetSummary(item));
    authKeys.addEventListener("portal-api-key-issue", () => {
      void this.issueAuthToken(authKeys);
    });
    authKeys.addEventListener("portal-api-key-revoke", (event: Event) => {
      const detail = (event as CustomEvent<{ id: string }>).detail;
      void this.revokeAuthToken(detail.id);
    });
    authSection.append(authKeys);

    const productSection = document.createElement("portal-detail-section");
    productSection.setAttribute("heading", "Product API keys");
    productSection.setAttribute("description", "Used by VTuber client applications outside of the browser portal.");
    const apiKeys = document.createElement("portal-api-keys") as unknown as PortalApiKeysElement;
    apiKeys.keys = (this.apiKeys ?? []).map((item) => this.toWidgetSummary(item));
    apiKeys.addEventListener("portal-api-key-issue", (event: Event) => {
      const detail = (event as CustomEvent<{ label: string }>).detail;
      void this.issueApiKey(apiKeys, detail.label);
    });
    apiKeys.addEventListener("portal-api-key-revoke", (event: Event) => {
      const detail = (event as CustomEvent<{ id: string }>).detail;
      void this.revokeApiKey(detail.id);
    });
    productSection.append(apiKeys);

    card.append(authSection, productSection);
    return card;
  }

  private billingView(): HTMLElement {
    const card = document.createElement("portal-card");
    card.setAttribute("heading", "Billing");
    card.setAttribute("subheading", "Shared prepaid account top-up history for this VTuber customer.");

    const tableShell = document.createElement("portal-data-table");
    tableShell.setAttribute("heading", "Top-up history");
    tableShell.setAttribute("description", "Settled and pending customer-fund events from the shared ledger.");

    const table = document.createElement("table");
    table.innerHTML = `
      <thead>
        <tr><th>Created</th><th>Amount</th><th>Status</th></tr>
      </thead>
      <tbody></tbody>
    `;
    const tbody = table.tBodies[0]!;
    for (const row of this.topups ?? []) {
      const tr = document.createElement("tr");
      const status = document.createElement("portal-status-pill");
      status.setAttribute("label", row.status);
      tr.append(
        this.textCell(row.created_at),
        this.textCell(this.formatUsd(row.amount_usd_cents)),
        this.nodeCell(status),
      );
      tbody.append(tr);
    }
    tableShell.append(table);
    card.append(tableShell);
    return card;
  }

  private textCell(text: string): HTMLTableCellElement {
    const td = document.createElement("td");
    td.textContent = text;
    return td;
  }

  private nodeCell(node: HTMLElement): HTMLTableCellElement {
    const td = document.createElement("td");
    td.append(node);
    return td;
  }

  private metaList(entries: Array<[string, string]>): HTMLElement {
    const list = document.createElement("dl");
    list.className = "vtuber-portal-meta-list";
    for (const [label, value] of entries) {
      const row = document.createElement("div");
      row.className = "vtuber-portal-meta-item";
      const dt = document.createElement("dt");
      dt.textContent = label;
      const dd = document.createElement("dd");
      dd.textContent = value;
      row.append(dt, dd);
      list.append(row);
    }
    return list;
  }

  private async loadPortalState(): Promise<void> {
    this.error = null;
    try {
      const [accountRes, limitsRes, authTokensRes, apiKeysRes, topupsRes] = await Promise.all([
        this.api.get<{ customer: PortalCustomer }>("/portal/account"),
        this.api.get<{ limits: PortalLimits }>("/portal/account/limits"),
        this.api.get<{ auth_tokens: CredentialSummary[] }>("/portal/auth-tokens"),
        this.api.get<{ api_keys: CredentialSummary[] }>("/portal/api-keys"),
        this.api.get<{ topups: TopupSummary[] }>("/portal/topups"),
      ]);
      this.customer = accountRes.customer;
      this.limits = limitsRes.limits;
      this.authTokens = authTokensRes.auth_tokens;
      this.apiKeys = apiKeysRes.api_keys;
      this.topups = topupsRes.topups;
    } catch (err) {
      this.error = this.errorMessage(err);
    }
    this.render();
  }

  private async issueAuthToken(widget: PortalApiKeysElement): Promise<void> {
    try {
      const response = await this.api.post<{ auth_token: string }>("/portal/auth-tokens");
      widget.showPlaintext(response.auth_token);
      await this.loadPortalState();
    } catch (err) {
      this.error = this.errorMessage(err);
      this.render();
    }
  }

  private async revokeAuthToken(id: string): Promise<void> {
    try {
      await this.api.request("DELETE", `/portal/auth-tokens/${encodeURIComponent(id)}`);
      await this.loadPortalState();
    } catch (err) {
      this.error = this.errorMessage(err);
      this.render();
    }
  }

  private async issueApiKey(widget: PortalApiKeysElement, label: string): Promise<void> {
    try {
      const response = await this.api.post<{ api_key: string }>("/portal/api-keys", label ? { label } : {});
      widget.showPlaintext(response.api_key);
      await this.loadPortalState();
    } catch (err) {
      this.error = this.errorMessage(err);
      this.render();
    }
  }

  private async revokeApiKey(id: string): Promise<void> {
    try {
      await this.api.request("DELETE", `/portal/api-keys/${encodeURIComponent(id)}`);
      await this.loadPortalState();
    } catch (err) {
      this.error = this.errorMessage(err);
      this.render();
    }
  }

  private toWidgetSummary(item: CredentialSummary): Record<string, string | null> {
    return {
      id: item.id,
      label: item.label,
      createdAt: item.created_at,
      lastUsedAt: item.last_used_at,
      revokedAt: item.revoked_at,
    };
  }

  private formatUsd(cents: string): string {
    return `$${(Number(cents) / 100).toFixed(2)}`;
  }

  private errorMessage(err: unknown): string {
    if (typeof err === "object" && err !== null && "body" in err) {
      const body = (err as { body?: unknown }).body;
      if (typeof body === "object" && body !== null && "message" in body && typeof (body as { message?: unknown }).message === "string") {
        return (body as { message: string }).message;
      }
      if (typeof body === "object" && body !== null && "error" in body && typeof (body as { error?: unknown }).error === "string") {
        return (body as { error: string }).error;
      }
    }
    return err instanceof Error ? err.message : "request_failed";
  }
}

if (!customElements.get("vtuber-gateway-portal")) {
  customElements.define("vtuber-gateway-portal", VtuberGatewayPortal);
}

declare global {
  interface HTMLElementTagNameMap {
    "vtuber-gateway-portal": VtuberGatewayPortal;
  }
}
