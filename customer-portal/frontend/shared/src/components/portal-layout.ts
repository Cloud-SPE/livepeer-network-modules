export class PortalLayout extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["brand"];
  }

  connectedCallback(): void {
    this.render();
  }

  attributeChangedCallback(): void {
    if (this.isConnected) {
      this.render();
    }
  }

  get brand(): string {
    return this.getAttribute("brand") ?? "Customer Portal";
  }

  set brand(value: string) {
    this.setAttribute("brand", value);
  }

  private render(): void {
    const header = this.ensureHeader();
    const main = this.ensureMain();
    const footer = this.ensureFooter();
    const nav = this.ensureNav(header);
    const brand = this.ensureBrand(header);
    const footerShell = this.ensureFooterShell(footer);

    brand.textContent = this.brand;

    const unmanaged = Array.from(this.childNodes).filter((node) => {
      return node !== header && node !== main && node !== footer;
    });

    for (const node of unmanaged) {
      if (node instanceof HTMLElement && node.getAttribute("slot") === "nav") {
        nav.append(node);
        node.removeAttribute("slot");
        continue;
      }
      if (node instanceof HTMLElement && node.getAttribute("slot") === "footer") {
        footerShell.append(node);
        node.removeAttribute("slot");
        continue;
      }
      main.append(node);
    }

    nav.hidden = nav.childNodes.length === 0;
    footer.hidden = footerShell.childNodes.length === 0;
  }

  private ensureHeader(): HTMLElement {
    let header = this.querySelector<HTMLElement>(":scope > .portal-layout__header");
    if (header !== null) {
      return header;
    }
    header = document.createElement("header");
    header.className = "portal-layout__header";
    const shell = document.createElement("div");
    shell.className = "portal-layout__header-shell";
    header.append(shell);
    this.append(header);
    return header;
  }

  private ensureBrand(header: HTMLElement): HTMLElement {
    const shell = header.querySelector<HTMLElement>(".portal-layout__header-shell")!;
    let brand = shell.querySelector<HTMLElement>(".portal-layout__brand");
    if (brand !== null) {
      return brand;
    }
    brand = document.createElement("div");
    brand.className = "portal-layout__brand";
    shell.append(brand);
    return brand;
  }

  private ensureNav(header: HTMLElement): HTMLElement {
    const shell = header.querySelector<HTMLElement>(".portal-layout__header-shell")!;
    let nav = shell.querySelector<HTMLElement>(".portal-layout__nav");
    if (nav !== null) {
      return nav;
    }
    nav = document.createElement("nav");
    nav.className = "portal-layout__nav";
    nav.setAttribute("aria-label", "Primary");
    shell.append(nav);
    return nav;
  }

  private ensureMain(): HTMLElement {
    let main = this.querySelector<HTMLElement>(":scope > .portal-layout__main");
    if (main !== null) {
      return main;
    }
    main = document.createElement("main");
    main.className = "portal-layout__main";
    this.append(main);
    return main;
  }

  private ensureFooter(): HTMLElement {
    let footer = this.querySelector<HTMLElement>(":scope > .portal-layout__footer");
    if (footer !== null) {
      return footer;
    }
    footer = document.createElement("footer");
    footer.className = "portal-layout__footer";
    this.append(footer);
    return footer;
  }

  private ensureFooterShell(footer: HTMLElement): HTMLElement {
    let shell = footer.querySelector<HTMLElement>(".portal-layout__footer-shell");
    if (shell !== null) {
      return shell;
    }
    shell = document.createElement("div");
    shell.className = "portal-layout__footer-shell";
    footer.append(shell);
    return shell;
  }
}

if (!customElements.get("portal-layout")) {
  customElements.define("portal-layout", PortalLayout);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-layout": PortalLayout;
  }
}
