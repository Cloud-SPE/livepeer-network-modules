import { ApiClient, HashRouter, clearSession, readSession } from "@livepeer-rewrite/customer-portal-shared";

interface RouteState {
  view: string;
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
  if (document.getElementById("video-gateway-portal-styles") !== null) {
    return;
  }
  const link = document.createElement("link");
  link.id = "video-gateway-portal-styles";
  link.rel = "stylesheet";
  link.href = new URL("./portal-app.css", import.meta.url).href;
  document.head.append(link);
}

export class VideoGatewayPortal extends HTMLElement {
  private route: RouteState = { view: "assets", params: {} };
  private authed = !!readSession()?.token && !!readSession()?.actor;
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
      .add("/assets", () => this.setRoute("assets"))
      .add("/streams", () => this.setRoute("streams"))
      .add("/webhooks", () => this.setRoute("webhooks"))
      .add("/recordings", () => this.setRoute("recordings"));
    if (!window.location.hash) {
      window.location.hash = "#/assets";
    }
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

  private setRoute(view: string, params: Record<string, string> = {}): void {
    this.route = { view, params };
    this.render();
  }

  private render(): void {
    this.replaceChildren(this.renderShell());
  }

  private renderShell(): HTMLElement {
    const layout = document.createElement("portal-layout");
    layout.setAttribute("brand", "Video Gateway Portal");
    if (this.authed) {
      const nav = document.createElement("nav");
      nav.slot = "nav";
      nav.className = "video-portal-nav";
      nav.setAttribute("aria-label", "Primary");
      nav.append(
        this.navLink("/assets", "Assets", "assets"),
        this.navLink("/streams", "Streams", "streams"),
        this.navLink("/recordings", "Recordings", "recordings"),
        this.navLink("/webhooks", "Webhooks", "webhooks"),
        this.navLink("/api-keys", "Keys", "api-keys"),
        this.navLink("/billing", "Billing", "billing"),
        this.navLink("/account", "Account", "account"),
      );
      const signOut = document.createElement("portal-button");
      signOut.setAttribute("variant", "ghost");
      signOut.textContent = "Sign out";
      signOut.addEventListener("click", () => {
        this.signOut();
      });
      nav.append(signOut);
      layout.append(nav);
    }

    const footer = document.createElement("span");
    footer.slot = "footer";
    footer.textContent = "Customer portal";

    const shell = document.createElement("div");
    shell.className = "video-portal-shell";
    shell.append(this.renderSummaryCard(), this.renderFeedback(), this.renderView());

    layout.append(shell, footer);
    return layout;
  }

  private navLink(to: string, label: string, key: string): HTMLAnchorElement {
    const link = document.createElement("a");
    link.href = `#${to}`;
    link.textContent = label;
    if (this.route.view === key) {
      link.className = "active";
    }
    return link;
  }

  private text<K extends keyof HTMLElementTagNameMap>(
    tag: K,
    className: string,
    value: string,
  ): HTMLElementTagNameMap[K] {
    const element = document.createElement(tag);
    element.className = className;
    element.textContent = value;
    return element;
  }

  private renderView(): HTMLElement {
    if (!this.authed && this.route.view !== "signup" && this.route.view !== "login") {
      return this.wrapCard(
        "Customer login",
        "Use an existing customer auth token and actor identity to access the video gateway portal.",
        this.loginElement(),
      );
    }

    switch (this.route.view) {
      case "signup":
        return this.wrapCard(
          "Create account",
          "Provision a customer account and receive the initial browser auth token.",
          this.signupElement(),
        );
      case "login":
        return this.wrapCard(
          "Customer login",
          "Use an existing customer auth token and actor identity to access the video gateway portal.",
          this.loginElement(),
        );
      case "account":
        return this.accountView();
      case "api-keys":
        return this.credentialsView();
      case "billing":
        return this.billingView();
      case "assets":
        return document.createElement("portal-assets");
      case "streams":
        return document.createElement("portal-streams");
      case "webhooks":
        return document.createElement("portal-webhooks");
      case "recordings":
        return document.createElement("portal-recordings");
      default:
        return this.text("p", "", "not found");
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

  private loginElement(): HTMLElement {
    const login = document.createElement("portal-login");
    login.addEventListener("portal-login-success", () => this.onSignedIn());
    return login;
  }

  private signupElement(): HTMLElement {
    const signup = document.createElement("portal-signup");
    signup.addEventListener("portal-signup-success", () => this.onSignedIn());
    return signup;
  }

  private onSignedIn(): void {
    this.authed = !!readSession()?.token && !!readSession()?.actor;
    this.render();
    void this.loadPortalState();
    window.location.hash = "#/assets";
  }

  private readonly onSessionChange = (): void => {
    this.authed = !!readSession()?.token && !!readSession()?.actor;
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

  private renderSummaryCard(): HTMLElement {
    const card = document.createElement("portal-card");
    card.setAttribute("heading", this.pageHeading());
    card.setAttribute("subheading", this.pageSubheading());

    if (!this.authed) {
      return card;
    }

    const grid = document.createElement("div");
    grid.className = "video-portal-session-grid";
    grid.append(
      this.sessionMeta(
        "Session",
        this.customer?.email ?? readSession()?.actor ?? "Authenticated",
        this.customer ? `${this.customer.tier} tier · ${this.customer.status}` : "Customer session active",
      ),
      this.sessionMeta(
        "Balance",
        this.formatUsd(this.customer?.balance_usd_cents ?? this.limits?.balance_usd_cents ?? "0"),
        `Reserved ${this.formatUsd(this.customer?.reserved_usd_cents ?? this.limits?.reserved_usd_cents ?? "0")}`,
      ),
    );
    card.append(grid);
    return card;
  }

  private renderFeedback(): HTMLElement {
    const wrapper = document.createElement("div");
    wrapper.className = "video-portal-feedback";
    if (this.error !== null) {
      const toast = document.createElement("portal-toast");
      toast.setAttribute("variant", "danger");
      toast.setAttribute("message", this.error);
      wrapper.append(toast);
    }
    return wrapper;
  }

  private sessionMeta(eyebrow: string, value: string, copy: string): HTMLElement {
    const section = document.createElement("div");
    section.className = "video-portal-session-meta";
    section.append(
      this.text("div", "video-portal-eyebrow", eyebrow),
      this.text("div", "video-portal-session-value", value),
      this.text("div", "video-portal-session-copy", copy),
    );
    return section;
  }

  private pageHeading(): string {
    switch (this.route.view) {
      case "login":
        return "Customer login";
      case "signup":
        return "Create account";
      case "account":
        return "Account";
      case "api-keys":
        return "Credentials";
      case "billing":
        return "Billing";
      case "assets":
        return "Assets";
      case "streams":
        return "Streams";
      case "webhooks":
        return "Webhooks";
      case "recordings":
        return "Recordings";
      default:
        return "Video Gateway Portal";
    }
  }

  private pageSubheading(): string {
    switch (this.route.view) {
      case "login":
        return "Authenticate with a customer auth token issued from the operator console.";
      case "signup":
        return "Create a customer account and start issuing tokens and API keys.";
      case "account":
        return "Identity, quota posture, and current balance for this customer.";
      case "api-keys":
        return "Manage browser auth tokens and application API keys separately.";
      case "billing":
        return "Review funding history and current prepaid balance posture.";
      case "assets":
        return "Manage uploaded assets, playback state, and restore deleted media.";
      case "streams":
        return "Create live streams and inspect RTMP ingest + playback details.";
      case "webhooks":
        return "Register webhook endpoints and inspect delivery state.";
      case "recordings":
        return "Manage record-to-VOD behavior and completed recordings.";
      default:
        return "Customer workflows for streams, assets, billing, and credentials.";
    }
  }

  private accountView(): HTMLElement {
    const balance = Number(this.customer?.balance_usd_cents ?? this.limits?.balance_usd_cents ?? "0");
    const reserved = Number(this.customer?.reserved_usd_cents ?? this.limits?.reserved_usd_cents ?? "0");

    const card = document.createElement("portal-card");
    card.setAttribute("heading", "Account");
    card.setAttribute("subheading", "Customer identity, quota posture, and current balance.");

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
    card.setAttribute("subheading", "Manage portal auth tokens and product API keys separately.");

    const authSection = document.createElement("portal-detail-section");
    authSection.setAttribute("heading", "UI auth tokens");
    authSection.setAttribute("description", "Used to log into browser portals across the monorepo.");
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

    const apiSection = document.createElement("portal-detail-section");
    apiSection.setAttribute("heading", "Product API keys");
    apiSection.setAttribute("description", "Used by client applications to call the video gateway.");
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
    apiSection.append(apiKeys);

    card.append(authSection, apiSection);
    return card;
  }

  private billingView(): HTMLElement {
    const card = document.createElement("portal-card");
    card.setAttribute("heading", "Billing");
    card.setAttribute("subheading", "Completed and pending top-ups tied to this customer account.");

    const tableShell = document.createElement("portal-data-table");
    tableShell.setAttribute("heading", "Top-up history");
    tableShell.setAttribute("description", "Gateway-funded balance events from the shared customer ledger.");
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
        this.cell(row.created_at),
        this.cell(this.formatUsd(row.amount_usd_cents)),
        this.nodeCell(status),
      );
      tbody.append(tr);
    }
    tableShell.append(table);
    card.append(tableShell);
    return card;
  }

  private metaList(entries: Array<[string, string]>): HTMLElement {
    const list = document.createElement("dl");
    list.className = "video-portal-meta-list";
    for (const [label, value] of entries) {
      const row = document.createElement("div");
      row.className = "video-portal-meta-item";
      const dt = document.createElement("dt");
      dt.textContent = label;
      const dd = document.createElement("dd");
      dd.textContent = value;
      row.append(dt, dd);
      list.append(row);
    }
    return list;
  }

  private cell(text: string): HTMLTableCellElement {
    const td = document.createElement("td");
    td.textContent = text;
    return td;
  }

  private nodeCell(node: HTMLElement): HTMLTableCellElement {
    const td = document.createElement("td");
    td.append(node);
    return td;
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

if (!customElements.get("video-gateway-portal")) {
  customElements.define("video-gateway-portal", VideoGatewayPortal);
}

declare global {
  interface HTMLElementTagNameMap {
    "video-gateway-portal": VideoGatewayPortal;
  }
}
