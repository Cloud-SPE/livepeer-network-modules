import { writeSession } from "../lib/session-storage.js";

export class PortalSignup extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["action", "default-actor"];
  }

  private errorMessage = "";

  connectedCallback(): void {
    this.render();
  }

  attributeChangedCallback(): void {
    if (this.isConnected) {
      this.render();
    }
  }

  get action(): string {
    return this.getAttribute("action") ?? "/portal/signup";
  }

  set action(value: string) {
    this.setAttribute("action", value);
  }

  get defaultActor(): string {
    return this.getAttribute("default-actor") ?? "customer";
  }

  set defaultActor(value: string) {
    this.setAttribute("default-actor", value);
  }

  private render(): void {
    const form = this.ensureForm();
    const emailInput = this.ensureEmailInput(form);
    const submit = this.ensureSubmit(form);
    const toast = this.ensureToast(form);

    emailInput.name = "email";
    emailInput.type = "email";
    emailInput.label = "Email";
    emailInput.required = true;

    submit.setAttribute("type", "submit");
    submit.setAttribute("block", "");
    submit.textContent = "Create account";

    if (this.errorMessage.length > 0) {
      toast.setAttribute("variant", "danger");
      toast.setAttribute("message", this.errorMessage);
      toast.hidden = false;
    } else {
      toast.hidden = true;
      toast.removeAttribute("message");
    }

    form.onsubmit = (event) => {
      void this.onSubmit(event);
    };
  }

  private ensureForm(): HTMLFormElement {
    let form = this.querySelector<HTMLFormElement>(":scope > .portal-signup__form");
    if (form !== null) {
      return form;
    }
    form = document.createElement("form");
    form.className = "portal-signup__form";
    this.append(form);
    return form;
  }

  private ensureEmailInput(
    form: HTMLFormElement,
  ): HTMLElement & { name: string; type: string; label: string; required: boolean } {
    let input = form.querySelector<HTMLElement>(":scope > portal-input[name='email']");
    if (input !== null) {
      return input as HTMLElement & {
        name: string;
        type: string;
        label: string;
        required: boolean;
      };
    }
    input = document.createElement("portal-input");
    form.append(input);
    return input as HTMLElement & {
      name: string;
      type: string;
      label: string;
      required: boolean;
    };
  }

  private ensureSubmit(form: HTMLFormElement): HTMLElement {
    let submit = form.querySelector<HTMLElement>(":scope > portal-button[type='submit']");
    if (submit !== null) {
      return submit;
    }
    submit = document.createElement("portal-button");
    form.append(submit);
    return submit;
  }

  private ensureToast(form: HTMLFormElement): HTMLElement {
    let toast = form.querySelector<HTMLElement>(":scope > portal-toast");
    if (toast !== null) {
      return toast;
    }
    toast = document.createElement("portal-toast");
    toast.hidden = true;
    form.append(toast);
    return toast;
  }

  private async onSubmit(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    const form = event.currentTarget as HTMLFormElement;
    const data = new FormData(form);
    const body = JSON.stringify({ email: data.get("email") });
    try {
      const response = await fetch(this.action, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body,
      });
      if (!response.ok) {
        const json = (await response.json().catch(() => ({}))) as {
          error?: { message?: string };
        };
        this.errorMessage = json.error?.message ?? `signup failed (${response.status})`;
        this.render();
        return;
      }
      const json = (await response.json()) as {
        auth_token?: string;
        actor?: string;
        customer?: { id?: string; email?: string };
      };
      if (json.auth_token) {
        writeSession({
          token: json.auth_token,
          actor: json.actor ?? this.defaultActor,
          ...(json.customer?.id ? { customerId: json.customer.id } : {}),
          ...(json.customer?.email ? { email: json.customer.email } : {}),
        });
      }
      this.errorMessage = "";
      this.render();
      this.dispatchEvent(
        new CustomEvent("portal-signup-success", {
          detail: json,
          bubbles: true,
          composed: true,
        }),
      );
    } catch (error) {
      this.errorMessage = error instanceof Error ? error.message : "signup failed";
      this.render();
    }
  }
}

if (!customElements.get("portal-signup")) {
  customElements.define("portal-signup", PortalSignup);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-signup": PortalSignup;
  }
}
