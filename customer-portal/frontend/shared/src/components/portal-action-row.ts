export class PortalActionRow extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["align"];
  }

  connectedCallback(): void {
    this.render();
  }

  attributeChangedCallback(): void {
    if (this.isConnected) {
      this.render();
    }
  }

  get align(): "start" | "end" {
    return this.getAttribute("align") === "end" ? "end" : "start";
  }

  set align(value: "start" | "end") {
    this.setAttribute("align", value);
  }

  private render(): void {
    const row = this.ensureRow();
    const unmanaged = Array.from(this.childNodes).filter((node) => node !== row);
    if (unmanaged.length > 0) {
      row.append(...unmanaged);
    }
  }

  private ensureRow(): HTMLElement {
    let row = this.querySelector<HTMLElement>(":scope > .portal-action-row__row");
    if (row !== null) {
      return row;
    }
    row = document.createElement("div");
    row.className = "portal-action-row__row";
    this.append(row);
    return row;
  }
}

if (!customElements.get("portal-action-row")) {
  customElements.define("portal-action-row", PortalActionRow);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-action-row": PortalActionRow;
  }
}
