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
  private authed = !!readSession()?.token;
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
    layout.setAttribute("brand", "Livepeer Network Console");

    const nav = document.createElement("nav");
    nav.slot = "nav";
    nav.className = "video-admin-nav";
    nav.setAttribute("aria-label", "Primary");
    if (!this.authed) {
      nav.append(this.navLink("/customers", "Sign in", "login"));
    }
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
    if (this.authed) {
      const signOut = document.createElement("a");
      signOut.href = "#/customers";
      signOut.textContent = "Sign out";
      signOut.addEventListener("click", (event) => {
        event.preventDefault();
        this.signOut();
      });
      nav.append(signOut);
    }

    const hero = document.createElement("section");
    hero.className = "video-admin-hero";
    hero.append(
      this.text("span", "video-admin-eyebrow", "Video Gateway"),
      this.text("h1", "video-admin-title", "Operator surface for streams, assets, and webhook delivery."),
      this.text(
        "p",
        "video-admin-lede",
        "This console keeps customer lifecycle, live ingest, VOD capture, and downstream delivery in one place. It uses the same Livepeer network language as the rest of the control plane.",
      ),
      this.metricGrid(),
    );

    const content = document.createElement("section");
    content.className = "video-admin-content";
    content.append(this.renderView());

    const footer = document.createElement("span");
    footer.slot = "footer";
    footer.textContent = "Livepeer video gateway operator console";

    layout.append(nav, hero, content, footer);
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

  private metricGrid(): HTMLElement {
    const grid = document.createElement("div");
    grid.className = "video-admin-metrics";
    grid.append(
      this.metricTile("Control Plane", "Network Console"),
      this.metricTile("Routing Model", "Manifest-driven"),
      this.metricTile("Primary Domain", "Video + VOD"),
    );
    return grid;
  }

  private metricTile(label: string, value: string): HTMLElement {
    const tile = document.createElement("portal-metric-tile");
    tile.setAttribute("label", label);
    tile.setAttribute("value", value);
    return tile;
  }

  private renderView(): HTMLElement {
    if (!this.authed) {
      const card = document.createElement("portal-card");
      card.setAttribute("heading", "Admin sign in");
      const form = document.createElement("form");
      form.className = "video-admin-login-form";
      form.addEventListener("submit", (event) => this.onAdminLogin(event));
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

  private onAdminLogin(event: Event): void {
    event.preventDefault();
    const form = new FormData(event.currentTarget as HTMLFormElement);
    writeSession({
      token: String(form.get("token") ?? ""),
      actor: String(form.get("actor") ?? ""),
    });
    this.authed = !!readSession()?.token;
    this.render();
    window.location.hash = "#/customers";
  }

  private readonly onSessionChange = (): void => {
    this.authed = !!readSession()?.token;
    this.render();
  };

  private signOut(): void {
    clearSession();
    this.authed = false;
    this.render();
    window.location.hash = "#/customers";
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
