export type ToastVariant = "info" | "success" | "warning" | "danger";

export class PortalToast extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["variant", "message"];
  }

  connectedCallback(): void {
    this.render();
  }

  attributeChangedCallback(): void {
    if (this.isConnected) {
      this.render();
    }
  }

  get variant(): ToastVariant {
    const value = this.getAttribute("variant");
    return value === "success" || value === "warning" || value === "danger" ? value : "info";
  }

  set variant(value: ToastVariant) {
    this.setAttribute("variant", value);
  }

  get message(): string {
    return this.getAttribute("message") ?? "";
  }

  set message(value: string) {
    this.setAttribute("message", value);
  }

  private render(): void {
    const body = this.ensureBody();
    const message = this.ensureMessage(body);
    const extra = this.ensureExtra(body);

    message.textContent = this.message;
    message.hidden = this.message.length === 0;
    this.setAttribute("role", this.variant === "danger" ? "alert" : "status");

    const unmanaged = Array.from(this.childNodes).filter((node) => node !== body);
    if (unmanaged.length > 0) {
      extra.append(...unmanaged);
    }
  }

  private ensureBody(): HTMLElement {
    let body = this.querySelector<HTMLElement>(":scope > .portal-toast__body");
    if (body !== null) {
      return body;
    }
    body = document.createElement("div");
    body.className = "portal-toast__body";
    this.append(body);
    return body;
  }

  private ensureMessage(body: HTMLElement): HTMLParagraphElement {
    let message = body.querySelector<HTMLParagraphElement>(".portal-toast__message");
    if (message !== null) {
      return message;
    }
    message = document.createElement("p");
    message.className = "portal-toast__message";
    body.append(message);
    return message;
  }

  private ensureExtra(body: HTMLElement): HTMLElement {
    let extra = body.querySelector<HTMLElement>(".portal-toast__extra");
    if (extra !== null) {
      return extra;
    }
    extra = document.createElement("div");
    extra.className = "portal-toast__extra";
    body.append(extra);
    return extra;
  }
}

if (!customElements.get("portal-toast")) {
  customElements.define("portal-toast", PortalToast);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-toast": PortalToast;
  }
}
