export class PortalBalance extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["currency", "balancecents", "reservedcents"];
  }

  connectedCallback(): void {
    this.render();
  }

  attributeChangedCallback(): void {
    if (this.isConnected) {
      this.render();
    }
  }

  get currency(): string {
    return this.getAttribute("currency") ?? "USD";
  }

  set currency(value: string) {
    this.setAttribute("currency", value);
  }

  get balanceCents(): number {
    return Number(this.getAttribute("balancecents") ?? "0");
  }

  set balanceCents(value: number) {
    this.setAttribute("balancecents", String(value));
  }

  get reservedCents(): number {
    return Number(this.getAttribute("reservedcents") ?? "0");
  }

  set reservedCents(value: number) {
    this.setAttribute("reservedcents", String(value));
  }

  private format(cents: number): string {
    const dollars = cents / 100;
    return dollars.toLocaleString(undefined, {
      style: "currency",
      currency: this.currency,
    });
  }

  private render(): void {
    this.replaceChildren();

    const row = document.createElement("div");
    row.className = "portal-balance__row";

    row.append(
      this.createStat("Available", this.format(this.balanceCents - this.reservedCents)),
      this.createStat("Reserved", this.format(this.reservedCents), "portal-balance__value--reserved"),
    );

    this.append(row);
  }

  private createStat(labelText: string, valueText: string, valueModifier = ""): HTMLElement {
    const stat = document.createElement("div");
    stat.className = "portal-balance__stat";

    const label = document.createElement("div");
    label.className = "portal-balance__label";
    label.textContent = labelText;

    const value = document.createElement("div");
    value.className = `portal-balance__value ${valueModifier}`.trim();
    value.textContent = valueText;

    stat.append(label, value);
    return stat;
  }
}

if (!customElements.get("portal-balance")) {
  customElements.define("portal-balance", PortalBalance);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-balance": PortalBalance;
  }
}
