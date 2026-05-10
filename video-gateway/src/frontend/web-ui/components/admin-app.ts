import { HashRouter, clearSession, readSession, writeSession } from "@livepeer-rewrite/customer-portal-shared";

export const VIDEO_GATEWAY_ADMIN_APP_TAG = "video-gateway-admin";

interface RouteState {
  view: string;
  params: Record<string, string>;
}

function installStyles(): void {
  if (document.getElementById("video-gateway-admin-styles") !== null) {
    return;
  }
  const link = document.createElement("link");
  link.id = "video-gateway-admin-styles";
  link.rel = "stylesheet";
  link.href = new URL("./admin-app.css", import.meta.url).href;
  document.head.append(link);
}

export class VideoGatewayAdmin extends HTMLElement {
  private route: RouteState = { view: "customers", params: {} };
  private authed = !!readSession()?.token && !!readSession()?.actor;
  private actor = readSession()?.actor ?? "";
  private error = "";
  private pending = false;
  private router: HashRouter | null = null;

  connectedCallback(): void {
    installStyles();
    this.router = new HashRouter();
    this.router
      .add("/health", () => this.setRoute("health"))
      .add("/customers", () => this.setRoute("customers"))
      .add("/customers/:id", (_p, params) => this.setRoute("customer-detail", params))
      .add("/customers/:id/adjust", (_p, params) => this.setRoute("customer-adjust", params))
      .add("/customers/:id/refund", (_p, params) => this.setRoute("customer-refund", params))
      .add("/assets", () => this.setRoute("assets"))
      .add("/topups", () => this.setRoute("topups"))
      .add("/reservations", () => this.setRoute("reservations"))
      .add("/audit", () => this.setRoute("audit"))
      .add("/streams", () => this.setRoute("streams"))
      .add("/webhooks", () => this.setRoute("webhooks"))
      .add("/recordings", () => this.setRoute("recordings"));
    if (!window.location.hash) {
      window.location.hash = "#/health";
    }
    this.router.start();
    window.addEventListener("storage", this.onSessionChange);
    this.render();
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
    layout.setAttribute("brand", "Video Gateway Admin");

    if (this.authed) {
      const nav = document.createElement("nav");
      nav.slot = "nav";
      nav.className = "video-admin-nav";
      nav.setAttribute("aria-label", "Primary");
      nav.append(
        this.navLink("/health", "Health", "health"),
        this.navLink("/customers", "Customers", "customers"),
        this.navLink("/topups", "Top-ups", "topups"),
        this.navLink("/reservations", "Reservations", "reservations"),
        this.navLink("/audit", "Audit", "audit"),
        this.navLink("/assets", "Assets", "assets"),
        this.navLink("/streams", "Streams", "streams"),
        this.navLink("/webhooks", "Webhooks", "webhooks"),
        this.navLink("/recordings", "Recordings", "recordings"),
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
    footer.textContent = "Operator console";

    const shell = document.createElement("div");
    shell.className = "video-admin-shell";
    shell.append(this.renderSummaryCard(), this.renderView());

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
    if (!this.authed) {
      const card = document.createElement("portal-card");
      card.setAttribute("heading", "Admin login");
      card.setAttribute("subheading", "Use the gateway admin token and actor identity to access operator workflows.");
      const form = document.createElement("form");
      form.className = "video-admin-login-form";
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
      if (this.pending) {
        button.setAttribute("loading", "");
      } else {
        button.removeAttribute("loading");
      }
      button.textContent = "Sign in";
      form.append(tokenInput, actorInput, button);
      card.append(form);
      const wrapper = document.createElement("div");
      wrapper.className = "video-admin-content";
      wrapper.append(card);
      if (this.error) {
        const toast = document.createElement("portal-toast");
        toast.setAttribute("variant", "danger");
        toast.setAttribute("message", this.error);
        wrapper.append(toast);
      }
      return wrapper;
    }

    const { view, params } = this.route;
    switch (view) {
      case "health":
        return document.createElement("admin-health");
      case "customers":
        return document.createElement("admin-customers");
      case "customer-detail": {
        const el = document.createElement("admin-customer-detail");
        el.setAttribute("customerId", params["id"] ?? "");
        return el;
      }
      case "customer-adjust": {
        const el = document.createElement("admin-customer-adjust");
        el.setAttribute("customerId", params["id"] ?? "");
        return el;
      }
      case "customer-refund": {
        const el = document.createElement("admin-customer-refund");
        el.setAttribute("customerId", params["id"] ?? "");
        return el;
      }
      case "assets":
        return document.createElement("admin-assets");
      case "topups":
        return document.createElement("admin-topups");
      case "reservations":
        return document.createElement("admin-reservations");
      case "audit":
        return document.createElement("admin-audit");
      case "streams":
        return document.createElement("admin-streams");
      case "webhooks":
        return document.createElement("admin-webhooks");
      case "recordings":
        return document.createElement("admin-recordings");
      default:
        return this.text("p", "", "not found");
    }
  }

  private renderSummaryCard(): HTMLElement {
    const card = document.createElement("portal-card");
    card.setAttribute("heading", this.pageHeading());
    card.setAttribute("subheading", this.pageSubheading());

    if (!this.authed) {
      return card;
    }

    const stack = document.createElement("div");
    stack.className = "video-admin-stack--sm";
    const session = document.createElement("div");
    session.className = "video-admin-session";
    const meta = document.createElement("div");
    meta.className = "video-admin-session-meta";
    meta.append(
      this.text("div", "video-admin-eyebrow", "Operator session"),
      this.text("div", "video-admin-session-value", this.actor),
    );
    session.append(meta);
    if (this.pending) {
      const toast = document.createElement("portal-toast");
      toast.setAttribute("variant", "info");
      toast.setAttribute("message", "Signing in...");
      session.append(toast);
    }
    stack.append(session);
    if (this.error) {
      const toast = document.createElement("portal-toast");
      toast.setAttribute("variant", "danger");
      toast.setAttribute("message", this.error);
      stack.append(toast);
    }
    card.append(stack);
    return card;
  }

  private async onAdminLogin(event: Event): Promise<void> {
    event.preventDefault();
    const form = new FormData(event.currentTarget as HTMLFormElement);
    const token = String(form.get("token") ?? "");
    const actor = String(form.get("actor") ?? "");
    this.pending = true;
    this.error = "";
    this.render();
    writeSession({ token, actor });
    try {
      const response = await fetch("/admin/assets", {
        headers: {
          authorization: `Bearer ${token}`,
          "x-actor": actor,
        },
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || `login failed (${response.status})`);
      }
      this.authed = true;
      this.actor = actor;
      window.location.hash = "#/customers";
    } catch (error) {
      clearSession();
      this.authed = false;
      this.actor = "";
      this.error = error instanceof Error ? error.message : "login failed";
    } finally {
      this.pending = false;
      this.render();
    }
  }

  private readonly onSessionChange = (): void => {
    const session = readSession();
    this.authed = !!session?.token && !!session?.actor;
    this.actor = session?.actor ?? "";
    this.render();
  };

  private signOut(): void {
    clearSession();
    this.authed = false;
    this.actor = "";
    this.error = "";
    this.render();
    window.location.hash = "#/health";
  }

  private pageHeading(): string {
    switch (this.route.view) {
      case "health":
        return "Health";
      case "customers":
      case "customer-detail":
      case "customer-adjust":
      case "customer-refund":
        return "Customers";
      case "topups":
        return "Top-ups";
      case "reservations":
        return "Reservations";
      case "audit":
        return "Audit";
      case "assets":
        return "Assets";
      case "streams":
        return "Streams";
      case "webhooks":
        return "Webhooks";
      case "recordings":
        return "Recordings";
      default:
        return "Video Gateway Admin";
    }
  }

  private pageSubheading(): string {
    switch (this.route.view) {
      case "health":
        return "Gateway readiness, operator auth, and static surface sanity checks.";
      case "customers":
      case "customer-detail":
      case "customer-adjust":
      case "customer-refund":
        return "Provision customers, inspect balances, and manage browser auth tokens and API keys.";
      case "topups":
        return "Review customer wallet funding and payment lifecycle events.";
      case "reservations":
        return "Inspect reserved, committed, and refunded work against the video gateway.";
      case "audit":
        return "Operator activity and customer-impacting administrative events.";
      case "assets":
        return "VOD asset lifecycle, storage state, and soft-delete recovery.";
      case "streams":
        return "Live ingest state, session lifecycle, and operator termination controls.";
      case "webhooks":
        return "Outbound delivery failures and replay workflows.";
      case "recordings":
        return "Record-to-VOD outputs and recording state transitions.";
      default:
        return "Operator workflows for streams, assets, customers, and webhook delivery.";
    }
  }
}

if (!customElements.get("video-gateway-admin")) {
  customElements.define("video-gateway-admin", VideoGatewayAdmin);
}

declare global {
  interface HTMLElementTagNameMap {
    "video-gateway-admin": VideoGatewayAdmin;
  }
}
