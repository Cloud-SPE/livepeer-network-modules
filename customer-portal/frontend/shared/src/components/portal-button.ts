export type PortalButtonVariant = "primary" | "ghost" | "danger";

export class PortalButton extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["variant", "block", "type", "disabled", "loading"];
  }

  connectedCallback(): void {
    this.render();
  }

  attributeChangedCallback(): void {
    if (this.isConnected) {
      this.render();
    }
  }

  get variant(): PortalButtonVariant {
    const value = this.getAttribute("variant");
    return value === "ghost" || value === "danger" ? value : "primary";
  }

  set variant(value: PortalButtonVariant) {
    this.setAttribute("variant", value);
  }

  get block(): boolean {
    return this.hasAttribute("block");
  }

  set block(value: boolean) {
    this.toggleAttribute("block", value);
  }

  get type(): "button" | "submit" | "reset" {
    const value = this.getAttribute("type");
    return value === "submit" || value === "reset" ? value : "button";
  }

  set type(value: "button" | "submit" | "reset") {
    this.setAttribute("type", value);
  }

  get disabled(): boolean {
    return this.hasAttribute("disabled");
  }

  set disabled(value: boolean) {
    this.toggleAttribute("disabled", value);
  }

  get loading(): boolean {
    return this.hasAttribute("loading");
  }

  set loading(value: boolean) {
    this.toggleAttribute("loading", value);
  }

  private render(): void {
    const control = this.ensureControl();
    const content = this.ensureContent(control);
    this.moveUnmanagedChildren(content);

    control.type = this.type;
    control.disabled = this.disabled || this.loading;
    control.setAttribute("aria-busy", this.loading ? "true" : "false");
    control.onclick = (event: MouseEvent) => this.onClick(event);

    const spinner = control.querySelector<HTMLElement>(".portal-button__spinner");
    if (this.loading && spinner === null) {
      const nextSpinner = document.createElement("span");
      nextSpinner.className = "portal-button__spinner";
      nextSpinner.setAttribute("aria-hidden", "true");
      control.insertBefore(nextSpinner, content);
    } else if (!this.loading && spinner !== null) {
      spinner.remove();
    }
  }

  private ensureControl(): HTMLButtonElement {
    let control = this.querySelector<HTMLButtonElement>(":scope > .portal-button__control");
    if (control !== null) {
      return control;
    }
    control = document.createElement("button");
    control.className = "portal-button__control";
    this.append(control);
    return control;
  }

  private ensureContent(control: HTMLButtonElement): HTMLSpanElement {
    let content = control.querySelector<HTMLSpanElement>(".portal-button__content");
    if (content !== null) {
      return content;
    }
    content = document.createElement("span");
    content.className = "portal-button__content";
    control.append(content);
    return content;
  }

  private moveUnmanagedChildren(content: HTMLElement): void {
    const unmanaged = Array.from(this.childNodes).filter((node) => {
      return node !== content.parentElement;
    });
    if (unmanaged.length > 0) {
      content.append(...unmanaged);
    }
  }

  private onClick(event: MouseEvent): void {
    if (this.disabled || this.loading) {
      event.preventDefault();
      event.stopPropagation();
      return;
    }
    if (this.type === "submit") {
      const form = this.closest("form");
      if (form !== null) {
        event.preventDefault();
        form.requestSubmit();
      }
    }
  }
}

if (!customElements.get("portal-button")) {
  customElements.define("portal-button", PortalButton);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-button": PortalButton;
  }
}
