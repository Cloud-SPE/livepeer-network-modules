export class PortalInput extends HTMLElement {
  static readonly formAssociated = true;

  static get observedAttributes(): string[] {
    return ["name", "label", "type", "value", "placeholder", "required", "error"];
  }

  private readonly internals =
    typeof this.attachInternals === "function" ? this.attachInternals() : null;

  connectedCallback(): void {
    this.render();
    this.syncFormState();
  }

  attributeChangedCallback(): void {
    if (this.isConnected) {
      this.render();
      this.syncFormState();
    }
  }

  get name(): string {
    return this.getAttribute("name") ?? "";
  }

  set name(value: string) {
    this.setAttribute("name", value);
  }

  get label(): string {
    return this.getAttribute("label") ?? "";
  }

  set label(value: string) {
    this.setAttribute("label", value);
  }

  get type(): string {
    return this.getAttribute("type") ?? "text";
  }

  set type(value: string) {
    this.setAttribute("type", value);
  }

  get value(): string {
    return this.getAttribute("value") ?? "";
  }

  set value(value: string) {
    this.setAttribute("value", value);
  }

  get placeholder(): string {
    return this.getAttribute("placeholder") ?? "";
  }

  set placeholder(value: string) {
    this.setAttribute("placeholder", value);
  }

  get required(): boolean {
    return this.hasAttribute("required");
  }

  set required(value: boolean) {
    this.toggleAttribute("required", value);
  }

  get error(): string {
    return this.getAttribute("error") ?? "";
  }

  set error(value: string) {
    this.setAttribute("error", value);
  }

  private render(): void {
    const label = this.ensureLabel();
    const input = this.ensureInput();
    const error = this.ensureError();

    const inputId = this.name || this.ensureStableId();
    label.textContent = this.label;
    label.htmlFor = inputId;
    label.hidden = this.label.length === 0;

    input.id = inputId;
    input.name = this.name;
    input.type = this.type;
    input.value = this.value;
    input.placeholder = this.placeholder;
    input.required = this.required;
    input.setAttribute("aria-invalid", this.error.length > 0 ? "true" : "false");
    if (this.error.length > 0) {
      input.setAttribute("aria-describedby", `${inputId}-error`);
      error.id = `${inputId}-error`;
    } else {
      input.removeAttribute("aria-describedby");
      error.removeAttribute("id");
    }

    error.textContent = this.error;
    error.hidden = this.error.length === 0;
  }

  private ensureLabel(): HTMLLabelElement {
    let label = this.querySelector<HTMLLabelElement>(":scope > .portal-input__label");
    if (label !== null) {
      return label;
    }
    label = document.createElement("label");
    label.className = "portal-input__label";
    this.append(label);
    return label;
  }

  private ensureInput(): HTMLInputElement {
    let input = this.querySelector<HTMLInputElement>(":scope > .portal-input__control");
    if (input !== null) {
      return input;
    }
    input = document.createElement("input");
    input.className = "portal-input__control";
    input.addEventListener("input", (event) => this.onInput(event));
    this.append(input);
    return input;
  }

  private ensureError(): HTMLElement {
    let error = this.querySelector<HTMLElement>(":scope > .portal-input__error");
    if (error !== null) {
      return error;
    }
    error = document.createElement("div");
    error.className = "portal-input__error";
    error.hidden = true;
    this.append(error);
    return error;
  }

  private onInput(event: Event): void {
    const target = event.target as HTMLInputElement;
    this.value = target.value;
    this.dispatchEvent(
      new CustomEvent("portal-input-change", {
        detail: { name: this.name, value: this.value },
        bubbles: true,
        composed: true,
      }),
    );
  }

  private syncFormState(): void {
    if (this.internals === null) {
      return;
    }
    if (typeof this.internals.setFormValue !== "function") {
      return;
    }
    this.internals.setFormValue(this.value);
    if (typeof this.internals.setValidity !== "function") {
      return;
    }
    if (this.required && !this.value) {
      this.internals.setValidity({ valueMissing: true }, "Please fill out this field.");
      return;
    }
    this.internals.setValidity({});
  }

  private ensureStableId(): string {
    let id = this.getAttribute("data-input-id");
    if (id !== null) {
      return id;
    }
    id = `portal-input-${crypto.randomUUID()}`;
    this.setAttribute("data-input-id", id);
    return id;
  }
}

if (!customElements.get("portal-input")) {
  customElements.define("portal-input", PortalInput);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-input": PortalInput;
  }
}
