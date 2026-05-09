export class PortalCard extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["heading", "subheading"];
  }

  connectedCallback(): void {
    this.render();
  }

  attributeChangedCallback(): void {
    if (this.isConnected) {
      this.render();
    }
  }

  get heading(): string {
    return this.getAttribute("heading") ?? "";
  }

  set heading(value: string) {
    this.setAttribute("heading", value);
  }

  get subheading(): string {
    return this.getAttribute("subheading") ?? "";
  }

  set subheading(value: string) {
    this.setAttribute("subheading", value);
  }

  private render(): void {
    const head = this.ensureHead();
    const heading = this.ensureHeading(head);
    const subheading = this.ensureSubheading(head);
    const body = this.ensureBody();

    heading.textContent = this.heading;
    subheading.textContent = this.subheading;
    head.hidden = this.heading.length === 0 && this.subheading.length === 0;

    const unmanaged = Array.from(this.childNodes).filter((node) => node !== head && node !== body);
    if (unmanaged.length > 0) {
      body.append(...unmanaged);
    }
  }

  private ensureHead(): HTMLElement {
    let head = this.querySelector<HTMLElement>(":scope > .portal-card__head");
    if (head !== null) {
      return head;
    }
    head = document.createElement("header");
    head.className = "portal-card__head";
    this.append(head);
    return head;
  }

  private ensureHeading(head: HTMLElement): HTMLHeadingElement {
    let heading = head.querySelector<HTMLHeadingElement>(".portal-card__heading");
    if (heading !== null) {
      return heading;
    }
    heading = document.createElement("h2");
    heading.className = "portal-card__heading";
    head.append(heading);
    return heading;
  }

  private ensureSubheading(head: HTMLElement): HTMLParagraphElement {
    let subheading = head.querySelector<HTMLParagraphElement>(".portal-card__subheading");
    if (subheading !== null) {
      return subheading;
    }
    subheading = document.createElement("p");
    subheading.className = "portal-card__subheading";
    head.append(subheading);
    return subheading;
  }

  private ensureBody(): HTMLElement {
    let body = this.querySelector<HTMLElement>(":scope > .portal-card__body");
    if (body !== null) {
      return body;
    }
    body = document.createElement("div");
    body.className = "portal-card__body";
    this.append(body);
    return body;
  }
}

if (!customElements.get("portal-card")) {
  customElements.define("portal-card", PortalCard);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-card": PortalCard;
  }
}
