import { writeSession } from "../lib/session-storage.js";

export class PortalLogin extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["action"];
  }

  private errorMessage = "";
  private pending = false;

  connectedCallback(): void {
    this.render();
  }

  attributeChangedCallback(): void {
    if (this.isConnected) {
      this.render();
    }
  }

  get action(): string {
    return this.getAttribute("action") ?? "/portal/login";
  }

  set action(value: string) {
    this.setAttribute("action", value);
  }

  private render(): void {
    const form = this.ensureForm();
    const tokenInput = this.ensureTokenInput(form);
    const actorInput = this.ensureActorInput(form);
    const toast = this.ensureToast(form);
    const submit = this.ensureSubmit(form);

    tokenInput.name = "token";
    tokenInput.label = "Auth token";
    tokenInput.required = true;

    actorInput.name = "actor";
    actorInput.label = "Actor";
    actorInput.required = true;

    submit.type = "submit";
    submit.disabled = this.pending;
    submit.setAttribute("block", "");
    submit.textContent = this.pending ? "Signing in..." : "Sign in";

    if (this.errorMessage.length > 0) {
      toast.setAttribute("variant", "danger");
      toast.setAttribute("message", this.errorMessage);
      toast.hidden = false;
    } else {
      toast.hidden = true;
      toast.removeAttribute("message");
    }

    if (toast.parentElement === form && toast.nextElementSibling !== submit) {
      form.insertBefore(toast, submit);
    }

    form.onsubmit = (event) => {
      void this.onSubmit(event);
    };
  }

  private ensureForm(): HTMLFormElement {
    let form = this.querySelector<HTMLFormElement>(":scope > .portal-login__form");
    if (form !== null) {
      return form;
    }
    form = document.createElement("form");
    form.className = "portal-login__form";
    this.append(form);
    return form;
  }

  private ensureTokenInput(form: HTMLFormElement): HTMLElement & { name: string; label: string; required: boolean } {
    let input = form.querySelector<HTMLElement>(":scope > portal-input[name='token']");
    if (input !== null) {
      return input as HTMLElement & { name: string; label: string; required: boolean };
    }
    input = document.createElement("portal-input");
    form.append(input);
    return input as HTMLElement & { name: string; label: string; required: boolean };
  }

  private ensureActorInput(form: HTMLFormElement): HTMLElement & { name: string; label: string; required: boolean } {
    let input = form.querySelector<HTMLElement>(":scope > portal-input[name='actor']");
    if (input !== null) {
      return input as HTMLElement & { name: string; label: string; required: boolean };
    }
    input = document.createElement("portal-input");
    form.append(input);
    return input as HTMLElement & { name: string; label: string; required: boolean };
  }

  private ensureSubmit(form: HTMLFormElement): HTMLButtonElement {
    let submit = form.querySelector<HTMLButtonElement>(":scope > .portal-auth-submit");
    if (submit !== null) {
      return submit;
    }
    submit = document.createElement("button");
    submit.className = "portal-auth-submit";
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
    this.pending = true;
    this.errorMessage = "";
    this.render();
    try {
      const response = await fetch(this.action, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          token: data.get("token"),
          actor: data.get("actor"),
        }),
      });
      if (!response.ok) {
        this.errorMessage = `login failed (${response.status})`;
        this.render();
        return;
      }
      const json = (await response.json()) as {
        actor: string;
        customer?: { id?: string; email?: string };
      };
      writeSession({
        token: String(data.get("token") ?? ""),
        actor: json.actor,
        ...(json.customer?.id ? { customerId: json.customer.id } : {}),
        ...(json.customer?.email ? { email: json.customer.email } : {}),
      });
      this.errorMessage = "";
      this.render();
      this.dispatchEvent(
        new CustomEvent("portal-login-success", { bubbles: true, composed: true }),
      );
    } catch (error) {
      this.errorMessage = error instanceof Error ? error.message : "login failed";
      this.render();
    } finally {
      this.pending = false;
      this.render();
    }
  }
}

if (!customElements.get("portal-login")) {
  customElements.define("portal-login", PortalLogin);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-login": PortalLogin;
  }
}
