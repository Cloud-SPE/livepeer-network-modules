export class PortalModal extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["open", "heading"];
  }

  connectedCallback(): void {
    this.render();
  }

  attributeChangedCallback(): void {
    if (this.isConnected) {
      this.render();
    }
  }

  get open(): boolean {
    return this.hasAttribute("open");
  }

  set open(value: boolean) {
    this.toggleAttribute("open", value);
  }

  get heading(): string {
    return this.getAttribute("heading") ?? "";
  }

  set heading(value: string) {
    this.setAttribute("heading", value);
  }

  private render(): void {
    const backdrop = this.ensureBackdrop();
    const panel = this.ensurePanel(backdrop);
    const heading = this.ensureHeading(panel);
    const body = this.ensureBody(panel);

    this.hidden = !this.open;
    this.setAttribute("role", "dialog");
    this.setAttribute("aria-modal", "true");

    heading.textContent = this.heading;
    heading.hidden = this.heading.length === 0;

    const unmanaged = Array.from(this.childNodes).filter((node) => node !== backdrop);
    if (unmanaged.length > 0) {
      body.append(...unmanaged);
    }
  }

  private ensureBackdrop(): HTMLElement {
    let backdrop = this.querySelector<HTMLElement>(":scope > .portal-modal__backdrop");
    if (backdrop !== null) {
      return backdrop;
    }
    backdrop = document.createElement("div");
    backdrop.className = "portal-modal__backdrop";
    this.append(backdrop);
    return backdrop;
  }

  private ensurePanel(backdrop: HTMLElement): HTMLElement {
    let panel = backdrop.querySelector<HTMLElement>(".portal-modal__panel");
    if (panel !== null) {
      return panel;
    }
    panel = document.createElement("section");
    panel.className = "portal-modal__panel";
    backdrop.append(panel);
    return panel;
  }

  private ensureHeading(panel: HTMLElement): HTMLHeadingElement {
    let heading = panel.querySelector<HTMLHeadingElement>(".portal-modal__heading");
    if (heading !== null) {
      return heading;
    }
    heading = document.createElement("h2");
    heading.className = "portal-modal__heading";
    panel.append(heading);
    return heading;
  }

  private ensureBody(panel: HTMLElement): HTMLElement {
    let body = panel.querySelector<HTMLElement>(".portal-modal__body");
    if (body !== null) {
      return body;
    }
    body = document.createElement("div");
    body.className = "portal-modal__body";
    panel.append(body);
    return body;
  }
}

if (!customElements.get("portal-modal")) {
  customElements.define("portal-modal", PortalModal);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-modal": PortalModal;
  }
}
