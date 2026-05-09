export interface ApiKeySummary {
  id: string;
  label: string | null;
  createdAt: string;
  lastUsedAt: string | null;
  revokedAt: string | null;
}

export class PortalApiKeys extends HTMLElement {
  private internalKeys: readonly ApiKeySummary[] = [];
  private newPlaintext: string | null = null;
  private newLabel = "";

  connectedCallback(): void {
    this.render();
  }

  get keys(): readonly ApiKeySummary[] {
    return this.internalKeys;
  }

  set keys(value: readonly ApiKeySummary[]) {
    this.internalKeys = value;
    if (this.isConnected) {
      this.render();
    }
  }

  showPlaintext(plaintext: string): void {
    this.newPlaintext = plaintext;
    this.newLabel = "";
    this.render();
  }

  private render(): void {
    const plaintext = this.ensurePlaintext();
    const plaintextValue = this.ensurePlaintextValue(plaintext);
    const form = this.ensureForm();
    const labelInput = this.ensureLabelInput(form);
    const issueButton = this.ensureIssueButton(form);
    const table = this.ensureTable();
    const tbody = this.ensureTableBody(table);

    if (this.newPlaintext !== null) {
      plaintext.hidden = false;
      plaintextValue.textContent = this.newPlaintext;
    } else {
      plaintext.hidden = true;
      plaintextValue.textContent = "";
    }

    labelInput.name = "label";
    labelInput.label = "";
    labelInput.placeholder = "Key label";
    labelInput.value = this.newLabel;

    issueButton.textContent = "Issue key";
    issueButton.onclick = () => this.onIssue();

    tbody.replaceChildren(...this.internalKeys.map((key) => this.renderRow(key)));
  }

  private ensurePlaintext(): HTMLElement {
    let plaintext = this.querySelector<HTMLElement>(":scope > .portal-api-keys__plaintext");
    if (plaintext !== null) {
      return plaintext;
    }
    plaintext = document.createElement("section");
    plaintext.className = "portal-api-keys__plaintext";
    const lead = document.createElement("strong");
    lead.textContent = "Save this key - it will not be shown again:";
    plaintext.append(lead);
    const value = document.createElement("div");
    value.className = "portal-api-keys__plaintext-value";
    plaintext.append(value);
    this.append(plaintext);
    return plaintext;
  }

  private ensurePlaintextValue(plaintext: HTMLElement): HTMLElement {
    return plaintext.querySelector<HTMLElement>(".portal-api-keys__plaintext-value")!;
  }

  private ensureForm(): HTMLElement {
    let form = this.querySelector<HTMLElement>(":scope > .portal-api-keys__form");
    if (form !== null) {
      return form;
    }
    form = document.createElement("div");
    form.className = "portal-api-keys__form";
    this.append(form);
    return form;
  }

  private ensureLabelInput(
    form: HTMLElement,
  ): HTMLElement & { name: string; label: string; placeholder: string; value: string } {
    let input = form.querySelector<HTMLElement>(":scope > portal-input");
    if (input !== null) {
      return input as HTMLElement & {
        name: string;
        label: string;
        placeholder: string;
        value: string;
      };
    }
    input = document.createElement("portal-input");
    input.addEventListener("portal-input-change", (event: Event) => {
      const detail = (event as CustomEvent<{ value: string }>).detail;
      this.newLabel = detail.value;
    });
    form.append(input);
    return input as HTMLElement & {
      name: string;
      label: string;
      placeholder: string;
      value: string;
    };
  }

  private ensureIssueButton(form: HTMLElement): HTMLElement {
    let button = form.querySelector<HTMLElement>(":scope > portal-button");
    if (button !== null) {
      return button;
    }
    button = document.createElement("portal-button");
    form.append(button);
    return button;
  }

  private ensureTable(): HTMLTableElement {
    let table = this.querySelector<HTMLTableElement>(":scope > .portal-api-keys__table");
    if (table !== null) {
      return table;
    }
    table = document.createElement("table");
    table.className = "portal-api-keys__table";
    table.innerHTML = `
      <thead>
        <tr>
          <th>Label</th>
          <th>Created</th>
          <th>Last used</th>
          <th>Status</th>
          <th aria-label="Actions"></th>
        </tr>
      </thead>
      <tbody></tbody>
    `;
    this.append(table);
    return table;
  }

  private ensureTableBody(table: HTMLTableElement): HTMLTableSectionElement {
    return table.tBodies[0] ?? table.createTBody();
  }

  private renderRow(key: ApiKeySummary): HTMLTableRowElement {
    const row = document.createElement("tr");
    row.append(
      this.renderCell(key.label ?? "(unlabeled)"),
      this.renderCell(key.createdAt),
      this.renderCell(key.lastUsedAt ?? "-"),
      this.renderCell(key.revokedAt ? "Revoked" : "Active"),
      this.renderActionCell(key),
    );
    return row;
  }

  private renderCell(value: string): HTMLTableCellElement {
    const cell = document.createElement("td");
    cell.textContent = value;
    return cell;
  }

  private renderActionCell(key: ApiKeySummary): HTMLTableCellElement {
    const cell = document.createElement("td");
    if (key.revokedAt) {
      return cell;
    }
    const button = document.createElement("portal-button");
    button.setAttribute("variant", "danger");
    button.textContent = "Revoke";
    button.onclick = () => this.onRevoke(key.id);
    cell.append(button);
    return cell;
  }

  private onIssue(): void {
    this.dispatchEvent(
      new CustomEvent("portal-api-key-issue", {
        detail: { label: this.newLabel },
        bubbles: true,
        composed: true,
      }),
    );
  }

  private onRevoke(id: string): void {
    this.dispatchEvent(
      new CustomEvent("portal-api-key-revoke", {
        detail: { id },
        bubbles: true,
        composed: true,
      }),
    );
  }
}

if (!customElements.get("portal-api-keys")) {
  customElements.define("portal-api-keys", PortalApiKeys);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-api-keys": PortalApiKeys;
  }
}
