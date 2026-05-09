export class PortalDetailSection extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["heading", "description"];
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

  get description(): string {
    return this.getAttribute("description") ?? "";
  }

  set description(value: string) {
    this.setAttribute("description", value);
  }

  private render(): void {
    const head = this.ensureHead();
    const heading = this.ensureHeading(head);
    const description = this.ensureDescription(head);
    const body = this.ensureBody();

    heading.textContent = this.heading;
    description.textContent = this.description;
    head.hidden = this.heading.length === 0 && this.description.length === 0;

    const unmanaged = Array.from(this.childNodes).filter((node) => node !== head && node !== body);
    if (unmanaged.length > 0) {
      body.append(...unmanaged);
    }
  }

  private ensureHead(): HTMLElement {
    let head = this.querySelector<HTMLElement>(":scope > .portal-detail-section__head");
    if (head !== null) {
      return head;
    }
    head = document.createElement("header");
    head.className = "portal-detail-section__head";
    this.append(head);
    return head;
  }

  private ensureHeading(head: HTMLElement): HTMLHeadingElement {
    let heading = head.querySelector<HTMLHeadingElement>(".portal-detail-section__heading");
    if (heading !== null) {
      return heading;
    }
    heading = document.createElement("h3");
    heading.className = "portal-detail-section__heading";
    head.append(heading);
    return heading;
  }

  private ensureDescription(head: HTMLElement): HTMLParagraphElement {
    let description = head.querySelector<HTMLParagraphElement>(".portal-detail-section__description");
    if (description !== null) {
      return description;
    }
    description = document.createElement("p");
    description.className = "portal-detail-section__description";
    head.append(description);
    return description;
  }

  private ensureBody(): HTMLElement {
    let body = this.querySelector<HTMLElement>(":scope > .portal-detail-section__body");
    if (body !== null) {
      return body;
    }
    body = document.createElement("div");
    body.className = "portal-detail-section__body";
    this.append(body);
    return body;
  }
}

if (!customElements.get("portal-detail-section")) {
  customElements.define("portal-detail-section", PortalDetailSection);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-detail-section": PortalDetailSection;
  }
}
