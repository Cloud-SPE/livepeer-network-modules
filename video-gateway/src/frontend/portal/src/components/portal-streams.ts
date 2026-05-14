import { ApiClient } from "@livepeer-network-modules/customer-portal-shared";

interface StreamRow {
  id: string;
  name: string;
  projectId: string;
  status: string;
  rtmpIngestUrl: string;
  playbackUrl: string;
  viewerCount: number | null;
  createdAt: string;
  endedAt: string | null;
}

interface CreatedStream extends StreamRow {
  sessionKey: string;
}

interface ProjectRow {
  id: string;
  name: string;
  isDefault: boolean;
}

function installStyles(): void {
  if (document.getElementById("video-gateway-portal-pages-styles") !== null) {
    return;
  }
  const link = document.createElement("link");
  link.id = "video-gateway-portal-pages-styles";
  link.rel = "stylesheet";
  link.href = new URL("./portal-pages.css", import.meta.url).href;
  document.head.append(link);
}

export class PortalStreams extends HTMLElement {
  private rows: StreamRow[] = [];
  private projects: ProjectRow[] = [];
  private selectedProjectId = "";
  private newName = "";
  private created: CreatedStream | null = null;
  private keyRevealed = false;
  private error: string | null = null;

  private readonly api = new ApiClient({ baseUrl: "" });

  connectedCallback(): void {
    installStyles();
    void this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      const query = this.selectedProjectId
        ? `?project_id=${encodeURIComponent(this.selectedProjectId)}`
        : "";
      const [out, projectsOut] = await Promise.all([
        this.api.get<{ items: StreamRow[] }>(`/portal/live-streams${query}`),
        this.api.get<{ items: ProjectRow[]; defaultProjectId: string }>("/portal/projects"),
      ]);
      this.rows = out.items ?? [];
      this.projects = projectsOut.items ?? [];
      if (!this.selectedProjectId && projectsOut.defaultProjectId) {
        this.selectedProjectId = projectsOut.defaultProjectId;
      }
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
    this.render();
  }

  private async createStream(event: Event): Promise<void> {
    event.preventDefault();
    if (this.newName.trim() === "") {
      return;
    }
    try {
      const out = await this.api.post<CreatedStream>("/portal/live-streams", {
        name: this.newName,
        project_id: this.selectedProjectId || undefined,
      });
      this.created = out;
      this.keyRevealed = true;
      this.newName = "";
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "create_failed";
      this.render();
    }
  }

  private async copyKey(): Promise<void> {
    if (this.created === null) {
      return;
    }
    try {
      await navigator.clipboard.writeText(this.created.sessionKey);
    } catch {
      // clipboard unavailable
    }
  }

  private async endStream(row: StreamRow): Promise<void> {
    if (!confirm(`End stream ${row.name}? Connected viewers will be disconnected.`)) {
      return;
    }
    try {
      await this.api.post(`/portal/live-streams/${encodeURIComponent(row.id)}/end`);
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "end_failed";
      this.render();
    }
  }

  private render(): void {
    const card = document.createElement("portal-card");
    card.setAttribute("heading", "Live streams");

    const tableShell = document.createElement("portal-data-table");
    tableShell.setAttribute("heading", "Stream Sessions");
    tableShell.setAttribute("description", "Create new stream keys, inspect playback URLs, and end live sessions.");

    const form = document.createElement("form");
    form.className = "video-portal-page-form";
    form.slot = "toolbar";
    form.addEventListener("submit", (event) => {
      void this.createStream(event);
    });
    const projectSelect = document.createElement("select");
    for (const project of this.projects) {
      const option = document.createElement("option");
      option.value = project.id;
      option.textContent = project.name;
      if (project.id === this.selectedProjectId) option.selected = true;
      projectSelect.append(option);
    }
    projectSelect.addEventListener("change", (event) => {
      this.selectedProjectId = (event.target as HTMLSelectElement).value;
      void this.load();
    });
    const input = document.createElement("input");
    input.placeholder = "stream name";
    input.value = this.newName;
    input.addEventListener("input", (event) => {
      this.newName = (event.target as HTMLInputElement).value;
    });
    const createButton = document.createElement("portal-button");
    createButton.setAttribute("type", "submit");
    createButton.textContent = "Create stream";
    form.append(projectSelect, input, createButton);
    tableShell.append(form);

    if (this.created !== null) {
      const createdCard = document.createElement("portal-card");
      createdCard.setAttribute("heading", "Stream created - copy session key now");
      createdCard.append(
        this.infoRow("Name", this.created.name),
        this.codeRow("RTMP ingest", this.created.rtmpIngestUrl),
        this.codeRow("LL-HLS playback", this.created.playbackUrl),
      );
      const reveal = document.createElement("div");
      reveal.className = "video-portal-page-reveal";
      reveal.append(
        document.createTextNode("Session key: "),
        this.message("span", this.keyRevealed ? this.created.sessionKey : "•••••••••••••", "video-portal-page-secret"),
      );
      createdCard.append(reveal);
      const actions = document.createElement("portal-action-row");
      const copyButton = document.createElement("portal-button");
      copyButton.setAttribute("variant", "ghost");
      copyButton.textContent = "Copy";
      copyButton.addEventListener("click", () => {
        void this.copyKey();
      });
      const dismissButton = document.createElement("portal-button");
      dismissButton.setAttribute("variant", "ghost");
      dismissButton.textContent = "Dismiss";
      dismissButton.addEventListener("click", () => {
        this.keyRevealed = false;
        this.created = null;
        this.render();
      });
      actions.append(copyButton, dismissButton);
      createdCard.append(actions, this.message("p", "This key is shown once. Store it before dismissing."));
      tableShell.append(createdCard);
    }

    if (this.error !== null) {
      tableShell.append(this.message("p", this.error, "video-portal-page-error"));
    }

    const table = document.createElement("table");
    table.className = "video-portal-page-table";
    table.innerHTML = `
      <thead>
        <tr><th>Name</th><th>Project</th><th>Status</th><th>Viewers</th><th>Playback</th><th>Started</th><th></th></tr>
      </thead>
      <tbody></tbody>
    `;
    const tbody = table.tBodies[0]!;
    for (const row of this.rows) {
      const tr = document.createElement("tr");
      tr.append(
        this.cell(row.name),
        this.cell(row.projectId),
        this.statusCell(row.status, row.status === "live" ? "success" : "neutral"),
        this.cell(row.viewerCount === null ? "-" : String(row.viewerCount)),
        this.codeCell(row.playbackUrl),
        this.cell(row.createdAt),
        this.streamActionCell(row),
      );
      tbody.append(tr);
    }
    tableShell.append(table);

    card.append(tableShell);
    this.replaceChildren(card);
  }

  private message<K extends keyof HTMLElementTagNameMap>(tag: K, text: string, className = ""): HTMLElementTagNameMap[K] {
    const element = document.createElement(tag);
    if (className !== "") {
      element.className = className;
    }
    element.textContent = text;
    return element;
  }

  private cell(text: string): HTMLTableCellElement {
    const td = document.createElement("td");
    td.textContent = text;
    return td;
  }

  private codeCell(text: string): HTMLTableCellElement {
    const td = document.createElement("td");
    const code = document.createElement("code");
    code.textContent = text;
    td.append(code);
    return td;
  }

  private statusCell(text: string, variant: string): HTMLTableCellElement {
    const td = document.createElement("td");
    const pill = document.createElement("portal-status-pill");
    pill.setAttribute("variant", variant);
    pill.textContent = text;
    td.append(pill);
    return td;
  }

  private streamActionCell(row: StreamRow): HTMLTableCellElement {
    const td = document.createElement("td");
    if (row.endedAt !== null) {
      td.textContent = "ended";
      return td;
    }
    const actionRow = document.createElement("portal-action-row");
    actionRow.setAttribute("align", "end");
    const button = document.createElement("portal-button");
    button.setAttribute("variant", "danger");
    button.textContent = "End";
    button.addEventListener("click", () => {
      void this.endStream(row);
    });
    actionRow.append(button);
    td.append(actionRow);
    return td;
  }

  private infoRow(label: string, value: string): HTMLElement {
    const p = document.createElement("p");
    const strong = document.createElement("strong");
    strong.textContent = `${label}: `;
    p.append(strong, document.createTextNode(value));
    return p;
  }

  private codeRow(label: string, value: string): HTMLElement {
    const p = document.createElement("p");
    const strong = document.createElement("strong");
    strong.textContent = `${label}: `;
    const code = document.createElement("code");
    code.textContent = value;
    p.append(strong, code);
    return p;
  }
}

if (!customElements.get("portal-streams")) {
  customElements.define("portal-streams", PortalStreams);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-streams": PortalStreams;
  }
}
