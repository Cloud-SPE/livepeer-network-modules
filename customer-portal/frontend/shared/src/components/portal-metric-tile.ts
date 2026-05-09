export class PortalMetricTile extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["label", "value", "detail"];
  }

  connectedCallback(): void {
    this.render();
  }

  attributeChangedCallback(): void {
    if (this.isConnected) {
      this.render();
    }
  }

  get label(): string {
    return this.getAttribute("label") ?? "";
  }

  set label(value: string) {
    this.setAttribute("label", value);
  }

  get value(): string {
    return this.getAttribute("value") ?? "";
  }

  set value(nextValue: string) {
    this.setAttribute("value", nextValue);
  }

  get detail(): string {
    return this.getAttribute("detail") ?? "";
  }

  set detail(value: string) {
    this.setAttribute("detail", value);
  }

  private render(): void {
    const label = this.ensureLabel();
    const value = this.ensureValue();
    const detail = this.ensureDetail();

    label.textContent = this.label;
    label.hidden = this.label.length === 0;

    const unmanaged = Array.from(this.childNodes).filter((node) => {
      return node !== label && node !== value && node !== detail;
    });
    let hasValueSlot = false;
    let hasDetailSlot = false;
    for (const node of unmanaged) {
      if (!(node instanceof HTMLElement)) {
        continue;
      }
      if (node.getAttribute("slot") === "value") {
        value.append(node);
        node.removeAttribute("slot");
        hasValueSlot = true;
        continue;
      }
      if (node.getAttribute("slot") === "detail") {
        detail.append(node);
        node.removeAttribute("slot");
        hasDetailSlot = true;
      }
    }

    if (this.value.length > 0) {
      value.textContent = this.value;
    }
    value.hidden = this.value.length === 0 && !hasValueSlot;

    if (this.detail.length > 0) {
      detail.textContent = this.detail;
    }
    detail.hidden = this.detail.length === 0 && !hasDetailSlot;
  }

  private ensureLabel(): HTMLElement {
    let label = this.querySelector<HTMLElement>(":scope > .portal-metric-tile__label");
    if (label !== null) {
      return label;
    }
    label = document.createElement("span");
    label.className = "portal-metric-tile__label";
    this.append(label);
    return label;
  }

  private ensureValue(): HTMLElement {
    let value = this.querySelector<HTMLElement>(":scope > .portal-metric-tile__value");
    if (value !== null) {
      return value;
    }
    value = document.createElement("span");
    value.className = "portal-metric-tile__value";
    this.append(value);
    return value;
  }

  private ensureDetail(): HTMLElement {
    let detail = this.querySelector<HTMLElement>(":scope > .portal-metric-tile__detail");
    if (detail !== null) {
      return detail;
    }
    detail = document.createElement("span");
    detail.className = "portal-metric-tile__detail";
    this.append(detail);
    return detail;
  }
}

if (!customElements.get("portal-metric-tile")) {
  customElements.define("portal-metric-tile", PortalMetricTile);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-metric-tile": PortalMetricTile;
  }
}
