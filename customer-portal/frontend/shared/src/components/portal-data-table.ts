export class PortalDataTable extends HTMLElement {
  static get observedAttributes(): string[] {
    return ["heading", "description", "empty", "empty-heading", "empty-message"];
  }

  connectedCallback(): void {
    this.render();
  }

  attributeChangedCallback(): void {
    if (this.isConnected) {
      this.render();
    }
  }

  get heading(): string {
    return this.getAttribute("heading") ?? "";
  }

  set heading(value: string) {
    this.setAttribute("heading", value);
  }

  get description(): string {
    return this.getAttribute("description") ?? "";
  }

  set description(value: string) {
    this.setAttribute("description", value);
  }

  get empty(): boolean {
    return this.hasAttribute("empty");
  }

  set empty(value: boolean) {
    this.toggleAttribute("empty", value);
  }

  get emptyHeading(): string {
    return this.getAttribute("empty-heading") ?? "Nothing here yet";
  }

  set emptyHeading(value: string) {
    this.setAttribute("empty-heading", value);
  }

  get emptyMessage(): string {
    return (
      this.getAttribute("empty-message") ??
      "Records will appear here once data is available."
    );
  }

  set emptyMessage(value: string) {
    this.setAttribute("empty-message", value);
  }

  private render(): void {
    const shell = this.ensureShell();
    const head = this.ensureHead(shell);
    const heading = this.ensureHeading(head);
    const description = this.ensureDescription(head);
    const toolbar = this.ensureToolbar(shell);
    const frame = this.ensureFrame(shell);
    const content = this.ensureContent(frame);
    const emptyState = this.ensureEmptyState(frame);
    const emptyHeading = this.ensureEmptyHeading(emptyState);
    const emptyMessage = this.ensureEmptyMessage(emptyState);

    heading.textContent = this.heading;
    description.textContent = this.description;
    head.hidden = this.heading.length === 0 && this.description.length === 0;

    emptyHeading.textContent = this.emptyHeading;
    emptyMessage.textContent = this.emptyMessage;
    content.hidden = this.empty;
    emptyState.hidden = !this.empty;

    const managed = new Set<Node>([shell, head, toolbar, frame, content, emptyState]);
    const unmanaged = Array.from(this.childNodes).filter((node) => !managed.has(node));

    for (const node of unmanaged) {
      if (node instanceof HTMLElement && node.getAttribute("slot") === "toolbar") {
        toolbar.append(node);
        node.removeAttribute("slot");
        continue;
      }
      content.append(node);
    }

    toolbar.hidden = toolbar.childNodes.length === 0;
  }

  private ensureShell(): HTMLElement {
    let shell = this.querySelector<HTMLElement>(":scope > .portal-data-table__shell");
    if (shell !== null) {
      return shell;
    }
    shell = document.createElement("section");
    shell.className = "portal-data-table__shell";
    this.append(shell);
    return shell;
  }

  private ensureHead(shell: HTMLElement): HTMLElement {
    let head = shell.querySelector<HTMLElement>(":scope > .portal-data-table__head");
    if (head !== null) {
      return head;
    }
    head = document.createElement("header");
    head.className = "portal-data-table__head";
    shell.append(head);
    return head;
  }

  private ensureHeading(head: HTMLElement): HTMLHeadingElement {
    let heading = head.querySelector<HTMLHeadingElement>(".portal-data-table__heading");
    if (heading !== null) {
      return heading;
    }
    heading = document.createElement("h3");
    heading.className = "portal-data-table__heading";
    head.append(heading);
    return heading;
  }

  private ensureDescription(head: HTMLElement): HTMLParagraphElement {
    let description = head.querySelector<HTMLParagraphElement>(".portal-data-table__description");
    if (description !== null) {
      return description;
    }
    description = document.createElement("p");
    description.className = "portal-data-table__description";
    head.append(description);
    return description;
  }

  private ensureToolbar(shell: HTMLElement): HTMLElement {
    let toolbar = shell.querySelector<HTMLElement>(":scope > .portal-data-table__toolbar");
    if (toolbar !== null) {
      return toolbar;
    }
    toolbar = document.createElement("div");
    toolbar.className = "portal-data-table__toolbar";
    shell.append(toolbar);
    return toolbar;
  }

  private ensureFrame(shell: HTMLElement): HTMLElement {
    let frame = shell.querySelector<HTMLElement>(":scope > .portal-data-table__frame");
    if (frame !== null) {
      return frame;
    }
    frame = document.createElement("div");
    frame.className = "portal-data-table__frame";
    shell.append(frame);
    return frame;
  }

  private ensureContent(frame: HTMLElement): HTMLElement {
    let content = frame.querySelector<HTMLElement>(":scope > .portal-data-table__content");
    if (content !== null) {
      return content;
    }
    content = document.createElement("div");
    content.className = "portal-data-table__content";
    frame.append(content);
    return content;
  }

  private ensureEmptyState(frame: HTMLElement): HTMLElement {
    let emptyState = frame.querySelector<HTMLElement>(":scope > .portal-data-table__empty");
    if (emptyState !== null) {
      return emptyState;
    }
    emptyState = document.createElement("div");
    emptyState.className = "portal-data-table__empty";
    emptyState.hidden = true;
    frame.append(emptyState);
    return emptyState;
  }

  private ensureEmptyHeading(emptyState: HTMLElement): HTMLElement {
    let heading = emptyState.querySelector<HTMLElement>(".portal-data-table__empty-heading");
    if (heading !== null) {
      return heading;
    }
    heading = document.createElement("div");
    heading.className = "portal-data-table__empty-heading";
    emptyState.append(heading);
    return heading;
  }

  private ensureEmptyMessage(emptyState: HTMLElement): HTMLElement {
    let message = emptyState.querySelector<HTMLElement>(".portal-data-table__empty-message");
    if (message !== null) {
      return message;
    }
    message = document.createElement("div");
    message.className = "portal-data-table__empty-message";
    emptyState.append(message);
    return message;
  }
}

if (!customElements.get("portal-data-table")) {
  customElements.define("portal-data-table", PortalDataTable);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-data-table": PortalDataTable;
  }
}
