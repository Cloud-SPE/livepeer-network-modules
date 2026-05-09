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

export class PortalVtuberPersonaPage extends HTMLElement {
  connectedCallback(): void {
    installPageStyles();
    this.render();
  }

  private render(): void {
    this.replaceChildren(
      this.card(
        "Persona Authoring",
        this.text(
          "p",
          "Shape the speaking style, system prompt, and scene behavior used when a session opens. This route should feel like a premium control surface, not a generic form dump.",
        ),
      ),
      this.panelGrid(),
    );
  }

  private panelGrid(): HTMLElement {
    const grid = this.wrap("div", "vtuber-portal-page-panel-grid");
    grid.append(
      this.panel("Prompt Core", "Voice definition", "Long-form persona description, guardrails, and conversational tone presets."),
      this.panel(
        "Realtime Behavior",
        "Session defaults",
        "Expression cadence, emotional range, and fallback behavior when model latency or transport quality degrades.",
      ),
      this.panel("Brand Consistency", "Reusable presets", "Saved persona kits for recurring shows, characters, or customer-facing demos."),
    );
    return grid;
  }

  private panel(eyebrow: string, title: string, body: string): HTMLElement {
    return this.wrap(
      "section",
      "vtuber-portal-page-panel",
      this.text("span", eyebrow, "vtuber-portal-page-eyebrow"),
      this.text("h3", title),
      this.text("p", body),
    );
  }

  private card(heading: string, ...children: Node[]): HTMLElement {
    const card = document.createElement("portal-card");
    card.setAttribute("heading", heading);
    card.append(...children);
    return card;
  }

  private text<K extends keyof HTMLElementTagNameMap>(tag: K, text: string, className = ""): HTMLElementTagNameMap[K] {
    const element = document.createElement(tag);
    if (className !== "") {
      element.className = className;
    }
    element.textContent = text;
    return element;
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

if (!customElements.get("portal-vtuber-persona")) {
  customElements.define("portal-vtuber-persona", PortalVtuberPersonaPage);
}
