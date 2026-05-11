import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";

interface ProjectUsage {
  assets: number;
  uploads: number;
  live_streams: number;
  webhooks: number;
}

interface ProjectRow {
  id: string;
  name: string;
  createdAt: string;
  isDefault: boolean;
  usage: ProjectUsage;
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
  private pending = false;

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
      this.error = this.errorMessage(err);
    }
    this.render();
  }

  private async createProject(event: Event): Promise<void> {
    event.preventDefault();
    const form = event.currentTarget as HTMLFormElement;
    const formData = new FormData(form);
    const name = String(formData.get("name") ?? "").trim();
    if (!name) return;
    this.pending = true;
    this.error = null;
    this.render();
    try {
      await this.api.post("/portal/projects", { name });
      form.reset();
      await this.load();
    } catch (err) {
      this.error = this.errorMessage(err);
      this.render();
    } finally {
      this.pending = false;
      this.render();
    }
  }

  private async renameProject(id: string, currentName: string): Promise<void> {
    const nextName = window.prompt("Rename project", currentName)?.trim();
    if (!nextName || nextName === currentName) return;
    this.pending = true;
    this.error = null;
    this.render();
    try {
      await this.api.request("PATCH", `/portal/projects/${encodeURIComponent(id)}`, {
        name: nextName,
      });
      await this.load();
    } catch (err) {
      this.error = this.errorMessage(err);
      this.render();
    } finally {
      this.pending = false;
      this.render();
    }
  }

  private async deleteProject(row: ProjectRow): Promise<void> {
    const confirmed = window.confirm(`Delete project "${row.name}"? This only works when it is empty.`);
    if (!confirmed) return;
    this.pending = true;
    this.error = null;
    this.render();
    try {
      await this.api.request("DELETE", `/portal/projects/${encodeURIComponent(row.id)}`);
      await this.load();
    } catch (err) {
      this.error = this.errorMessage(err);
      this.render();
    } finally {
      this.pending = false;
      this.render();
    }
  }

  private render(): void {
    const card = document.createElement("portal-card");
    card.setAttribute("heading", "Projects");
    card.setAttribute(
      "subheading",
      "Projects own assets, streams, recordings, and webhooks. Empty projects can be removed.",
    );

    const createSection = document.createElement("portal-detail-section");
    createSection.setAttribute("heading", "Create project");
    createSection.setAttribute(
      "description",
      this.defaultProjectId !== "" ? `Current default target: ${this.defaultProjectId}` : "Create a project to start organizing media resources.",
    );
    const form = document.createElement("form");
    form.className = "video-portal-page-form";
    form.addEventListener("submit", (event) => {
      void this.createProject(event);
    });
    const input = document.createElement("portal-input");
    input.setAttribute("name", "name");
    input.setAttribute("label", "Project name");
    input.setAttribute("required", "");
    const button = document.createElement("portal-button");
    button.setAttribute("type", "submit");
    button.textContent = this.pending ? "Working..." : "Create project";
    if (this.pending) button.setAttribute("loading", "");
    form.append(input, button);
    createSection.append(form);
    if (this.error !== null) {
      const toast = document.createElement("portal-toast");
      toast.setAttribute("variant", "danger");
      toast.setAttribute("message", this.error);
      createSection.append(toast);
    }
    card.append(createSection);

    const tableShell = document.createElement("portal-data-table");
    tableShell.setAttribute("heading", "Project list");
    tableShell.setAttribute(
      "description",
      "Rename projects as needed. Deletion is blocked while they still own media or webhooks.",
    );

    const table = document.createElement("table");
    table.className = "video-portal-page-table";
    table.innerHTML = `
      <thead>
        <tr><th>Name</th><th>ID</th><th>Created</th><th>Role</th><th>Usage</th><th></th></tr>
      </thead>
      <tbody></tbody>
    `;
    const tbody = table.tBodies[0]!;
    for (const row of this.rows) {
      const tr = document.createElement("tr");
      const actionCell = document.createElement("td");
      const rename = document.createElement("portal-button");
      rename.setAttribute("variant", "ghost");
      rename.textContent = "Rename";
      rename.addEventListener("click", () => {
        void this.renameProject(row.id, row.name);
      });
      actionCell.append(rename);
      if (!row.isDefault) {
        const destroy = document.createElement("portal-button");
        destroy.setAttribute("variant", "danger");
        destroy.textContent = "Delete";
        destroy.addEventListener("click", () => {
          void this.deleteProject(row);
        });
        actionCell.append(destroy);
      }
      tr.append(
        this.cell(row.name),
        this.codeCell(row.id),
        this.cell(row.createdAt),
        this.cell(row.isDefault ? "Default" : "Project"),
        this.cell(
          `${row.usage.assets} assets · ${row.usage.live_streams} streams · ${row.usage.webhooks} webhooks`,
        ),
        actionCell,
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

  private errorMessage(err: unknown): string {
    if (typeof err === "object" && err !== null && "body" in err) {
      const body = (err as { body?: unknown }).body;
      if (typeof body === "object" && body !== null && "error" in body && typeof (body as { error?: unknown }).error === "string") {
        return (body as { error: string }).error;
      }
      if (typeof body === "object" && body !== null && "message" in body && typeof (body as { message?: unknown }).message === "string") {
        return (body as { message: string }).message;
      }
    }
    return err instanceof Error ? err.message : "request_failed";
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
