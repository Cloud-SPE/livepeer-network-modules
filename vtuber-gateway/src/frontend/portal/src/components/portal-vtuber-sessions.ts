function installPageStyles(): void {
  if (document.getElementById("vtuber-gateway-portal-page-styles") !== null) {
    return;
  }
  const link = document.createElement("link");
  link.id = "vtuber-gateway-portal-page-styles";
  link.rel = "stylesheet";
  link.href = new URL("./portal-vtuber-pages.css", import.meta.url).href;
  document.head.append(link);
}

export class PortalVtuberSessionsPage extends HTMLElement {
  connectedCallback(): void {
    installPageStyles();
    this.render();
  }

  private render(): void {
    this.replaceChildren(
      this.card(
        "My VTuber Sessions",
        this.wrap(
          "div",
          "vtuber-portal-page-intro",
          this.wrap("span", "vtuber-portal-page-pill", document.createTextNode("Realtime session plane")),
          this.wrap(
            "p",
            "",
            document.createTextNode(
              "Active and historical persona sessions land here. This route is where operators watch burn-rate, revisit prior scenes, and top up a live session before balance pressure ends it.",
            ),
          ),
        ),
      ),
      this.stats(),
      this.queueCard(),
    );
  }

  private stats(): HTMLElement {
    const grid = this.wrap("div", "vtuber-portal-page-stats");
    grid.append(
      this.stat("Current Surface", "Session Ledger"),
      this.stat("Pricing Rhythm", "Per-second + top-up"),
      this.stat("Control Mode", "WS + media plane"),
    );
    return grid;
  }

  private stat(label: string, value: string): HTMLElement {
    return this.wrap(
      "div",
      "vtuber-portal-page-stat",
      this.wrap("span", "vtuber-portal-page-stat-label", document.createTextNode(label)),
      this.wrap("span", "vtuber-portal-page-stat-value", document.createTextNode(value)),
    );
  }

  private queueCard(): HTMLElement {
    return this.card(
      "Upcoming UI Work",
      this.wrap(
        "div",
        "vtuber-portal-page-queue",
        this.queueItem("Active sessions table", "Session status, current balance runway, top-up action, and end-session control."),
        this.queueItem("History timeline", "Recent sessions with persona, scene source, and reconnect / balance events."),
        this.queueItem("Control diagnostics", "Surface broker control events like session.balance.low and refill confirmations."),
      ),
    );
  }

  private queueItem(title: string, body: string): HTMLElement {
    return this.wrap(
      "div",
      "vtuber-portal-page-queue-item",
      this.wrap("div", "vtuber-portal-page-queue-title", document.createTextNode(title)),
      this.wrap("p", "", document.createTextNode(body)),
    );
  }

  private card(heading: string, ...children: Node[]): HTMLElement {
    const card = document.createElement("portal-card");
    card.setAttribute("heading", heading);
    card.append(...children);
    return card;
  }

  private wrap<K extends keyof HTMLElementTagNameMap>(tag: K, className: string, ...children: Node[]): HTMLElementTagNameMap[K] {
    const element = document.createElement(tag);
    if (className !== "") {
      element.className = className;
    }
    element.append(...children);
    return element;
  }
}

if (!customElements.get("portal-vtuber-sessions")) {
  customElements.define("portal-vtuber-sessions", PortalVtuberSessionsPage);
}
