import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";

interface ProjectRow {
  id: string;
  name: string;
  createdAt: string;
  isDefault: boolean;
}

interface ProjectListResponse {
  items: ProjectRow[];
  defaultProjectId: string;
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

export class PortalProjects extends HTMLElement {
  private rows: ProjectRow[] = [];
  private defaultProjectId = "";
  private error: string | null = null;

  private readonly api = new ApiClient({ baseUrl: "" });

  connectedCallback(): void {
    installStyles();
    void this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      const out = await this.api.get<ProjectListResponse>("/portal/projects");
      this.rows = out.items ?? [];
      this.defaultProjectId = out.defaultProjectId ?? "";
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
    this.render();
  }

  private render(): void {
    const card = document.createElement("portal-card");
    card.setAttribute("heading", "Projects");
    card.setAttribute(
      "subheading",
      "Portal-created uploads and live streams land in the default project unless a future project picker overrides it.",
    );

    const tableShell = document.createElement("portal-data-table");
    tableShell.setAttribute("heading", "Project list");
    tableShell.setAttribute(
      "description",
      this.defaultProjectId !== ""
        ? `Default target: ${this.defaultProjectId}`
        : "No projects available.",
    );

    if (this.error !== null) {
      const p = document.createElement("p");
      p.className = "video-portal-page-error";
      p.textContent = this.error;
      tableShell.append(p);
    }

    const table = document.createElement("table");
    table.className = "video-portal-page-table";
    table.innerHTML = `
      <thead>
        <tr><th>Name</th><th>ID</th><th>Created</th><th>Role</th></tr>
      </thead>
      <tbody></tbody>
    `;
    const tbody = table.tBodies[0]!;
    for (const row of this.rows) {
      const tr = document.createElement("tr");
      tr.append(
        this.cell(row.name),
        this.codeCell(row.id),
        this.cell(row.createdAt),
        this.cell(row.isDefault ? "Default" : "Project"),
      );
      tbody.append(tr);
    }
    tableShell.append(table);
    card.append(tableShell);
    this.replaceChildren(card);
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
}

if (!customElements.get("portal-projects")) {
  customElements.define("portal-projects", PortalProjects);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-projects": PortalProjects;
  }
}
