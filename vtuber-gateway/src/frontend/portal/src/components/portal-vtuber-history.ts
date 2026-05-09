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

export class PortalVtuberHistoryPage extends HTMLElement {
  connectedCallback(): void {
    installPageStyles();
    this.render();
  }

  private render(): void {
    this.replaceChildren(
      this.card(
        "Scene History",
        this.text(
          "p",
          "Recent VRM selections, target broadcast contexts, and session-to-session continuity live here. This is the narrative memory of the VTuber product.",
        ),
      ),
      this.timeline(),
    );
  }

  private timeline(): HTMLElement {
    const timeline = this.wrap("div", "vtuber-portal-page-timeline");
    timeline.append(
      this.entry(
        ["Recent session", "VRM continuity"],
        "Character + stream context ledger",
        "Keep a readable archive of model, scene source, and broadcast target changes over time.",
      ),
      this.entry(
        ["Operator memory", "Replayable state"],
        "Reusable scene packets",
        "Bring past shows back quickly with archived persona, visuals, and runtime control defaults.",
      ),
    );
    return timeline;
  }

  private entry(meta: string[], title: string, body: string): HTMLElement {
    const metaWrap = this.wrap("div", "vtuber-portal-page-meta");
    for (const item of meta) {
      metaWrap.append(this.text("span", item));
    }
    return this.wrap(
      "section",
      "vtuber-portal-page-entry",
      metaWrap,
      this.text("div", title, "vtuber-portal-page-entry-title"),
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

if (!customElements.get("portal-vtuber-history")) {
  customElements.define("portal-vtuber-history", PortalVtuberHistoryPage);
}
