import { readSession } from "../lib/session-storage.js";

export class PortalCheckoutButton extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["action", "amount-cents", "publishable-key", "auth-token"];
  }

  private loading = false;

  connectedCallback(): void {
    this.render();
  }

  attributeChangedCallback(): void {
    if (this.isConnected) {
      this.render();
    }
  }

  get action(): string {
    return this.getAttribute("action") ?? "/v1/billing/topup/checkout";
  }

  set action(value: string) {
    this.setAttribute("action", value);
  }

  get amountCents(): number {
    return Number(this.getAttribute("amount-cents") ?? "1000");
  }

  set amountCents(value: number) {
    this.setAttribute("amount-cents", String(value));
  }

  get publishableKey(): string {
    return this.getAttribute("publishable-key") ?? "";
  }

  set publishableKey(value: string) {
    this.setAttribute("publishable-key", value);
  }

  get authToken(): string {
    return this.getAttribute("auth-token") ?? "";
  }

  set authToken(value: string) {
    this.setAttribute("auth-token", value);
  }

  private render(): void {
    const button = this.ensureButton();
    button.loading = this.loading;
    button.disabled = this.loading;
    button.onclick = () => {
      void this.onClick();
    };

    const unmanaged = Array.from(this.childNodes).filter((node) => node !== button);
    if (unmanaged.length > 0) {
      button.append(...unmanaged);
    }

    if (button.textContent?.trim().length === 0) {
      button.textContent = "Top up";
    }
  }

  private ensureButton(): HTMLElement & { loading?: boolean; disabled?: boolean } {
    let button = this.querySelector<HTMLElement>(":scope > portal-button");
    if (button !== null) {
      return button as HTMLElement & { loading?: boolean; disabled?: boolean };
    }
    button = document.createElement("portal-button");
    this.append(button);
    return button as HTMLElement & { loading?: boolean; disabled?: boolean };
  }

  private async onClick(): Promise<void> {
    this.loading = true;
    this.render();
    try {
      const headers: Record<string, string> = { "content-type": "application/json" };
      const session = readSession();
      const token = this.authToken || session?.token || "";
      const actor = session?.actor || "";
      if (token) headers.authorization = `Bearer ${token}`;
      if (actor) headers["x-actor"] = actor;

      const response = await fetch(this.action, {
        method: "POST",
        headers,
        body: JSON.stringify({ amount_usd_cents: this.amountCents }),
      });
      if (!response.ok) {
        throw new Error(`checkout init failed: ${response.status}`);
      }
      const json = (await response.json()) as { url?: string };
      if (!json.url) {
        throw new Error("checkout init returned no url");
      }
      window.location.assign(json.url);
    } catch (error) {
      this.dispatchEvent(
        new CustomEvent("portal-checkout-error", {
          detail: {
            message: error instanceof Error ? error.message : "checkout failed",
          },
          bubbles: true,
          composed: true,
        }),
      );
    } finally {
      this.loading = false;
      this.render();
    }
  }
}

if (!customElements.get("portal-checkout-button")) {
  customElements.define("portal-checkout-button", PortalCheckoutButton);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-checkout-button": PortalCheckoutButton;
  }
}
