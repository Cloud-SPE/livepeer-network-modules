export type PortalStatusPillVariant =
  | "neutral"
  | "info"
  | "success"
  | "warning"
  | "danger";

export class PortalStatusPill extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["variant"];
  }

  connectedCallback(): void {
    this.setAttribute("role", "status");
  }

  attributeChangedCallback(): void {
    if (!this.isConnected) {
      return;
    }
    this.setAttribute("role", "status");
  }

  get variant(): PortalStatusPillVariant {
    const value = this.getAttribute("variant");
    if (value === "info" || value === "success" || value === "warning" || value === "danger") {
      return value;
    }
    return "neutral";
  }

  set variant(value: PortalStatusPillVariant) {
    this.setAttribute("variant", value);
  }
}

if (!customElements.get("portal-status-pill")) {
  customElements.define("portal-status-pill", PortalStatusPill);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-status-pill": PortalStatusPill;
  }
}
