import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";

interface AssetRow {
  id: string;
  status: string;
  projectId: string;
  durationSec: number | null;
  createdAt: string;
  deletedAt: string | null;
  playbackUrl: string | null;
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

export class PortalAssets extends HTMLElement {
  private rows: AssetRow[] = [];
  private projects: ProjectRow[] = [];
  private selectedProjectId = "";
  private uploading = false;
  private error: string | null = null;
  private confirmDelete: AssetRow | null = null;

  private readonly api = new ApiClient({ baseUrl: "" });

  connectedCallback(): void {
    installStyles();
    void this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      const projectQuery = this.selectedProjectId
        ? `?project_id=${encodeURIComponent(this.selectedProjectId)}`
        : "";
      const [out, projectsOut] = await Promise.all([
        this.api.get<{ items: AssetRow[] }>(`/portal/assets${projectQuery}`),
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

  private async upload(file: File): Promise<void> {
    this.uploading = true;
    this.error = null;
    this.render();
    try {
      const init = await this.api.post<{ uploadUrl: string; assetId: string }>("/portal/uploads", {
        filename: file.name,
        size: file.size,
        contentType: file.type,
        project_id: this.selectedProjectId || undefined,
      });
      const tus = await fetch(init.uploadUrl, {
        method: "PATCH",
        headers: {
          "tus-resumable": "1.0.0",
          "upload-offset": "0",
          "content-type": "application/offset+octet-stream",
        },
        body: file,
      });
      if (!tus.ok) {
        throw new Error(`upload_failed_${tus.status}`);
      }
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "upload_failed";
      this.render();
    } finally {
      this.uploading = false;
      this.render();
    }
  }

  private onFile(event: Event): void {
    const input = event.target as HTMLInputElement;
    const file = input.files?.[0];
    if (file !== undefined) {
      void this.upload(file);
    }
    input.value = "";
  }

  private async softDelete(row: AssetRow): Promise<void> {
    try {
      await this.api.request("DELETE", `/portal/assets/${encodeURIComponent(row.id)}`);
      this.confirmDelete = null;
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "delete_failed";
      this.render();
    }
  }

  private async restore(row: AssetRow): Promise<void> {
    try {
      await this.api.post(`/portal/assets/${encodeURIComponent(row.id)}/restore`);
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "restore_failed";
      this.render();
    }
  }

  private render(): void {
    const card = document.createElement("portal-card");
    card.setAttribute("heading", "Asset library");

    const tableShell = document.createElement("portal-data-table");
    tableShell.setAttribute("heading", "Library");
    tableShell.setAttribute("description", "Upload, review, restore, and retire video assets from one place.");

    const toolbar = document.createElement("div");
    toolbar.className = "video-portal-page-toolbar";
    toolbar.slot = "toolbar";
    const projectSelect = document.createElement("select");
    const allOption = document.createElement("option");
    allOption.value = "";
    allOption.textContent = "All projects";
    projectSelect.append(allOption);
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
    input.type = "file";
    input.disabled = this.uploading;
    input.addEventListener("change", (event) => this.onFile(event));
    toolbar.append(projectSelect, input);
    if (this.uploading) {
      toolbar.append(this.message("span", "Uploading."));
    }
    tableShell.append(toolbar);

    if (this.error !== null) {
      tableShell.append(this.message("p", this.error, "video-portal-page-error"));
    }

    const table = document.createElement("table");
    table.className = "video-portal-page-table";
    table.innerHTML = `
      <thead>
        <tr><th>ID</th><th>Project</th><th>Status</th><th>Duration</th><th>Created</th><th>Playback</th><th></th></tr>
      </thead>
      <tbody></tbody>
    `;
    const tbody = table.tBodies[0]!;
    for (const row of this.rows) {
      const tr = document.createElement("tr");
      if (row.deletedAt !== null) {
        tr.className = "video-portal-page-deleted";
      }
      tr.append(
        this.cell(row.id),
        this.cell(row.projectId),
        this.cell(`${row.status}${row.deletedAt !== null ? " (deleted)" : ""}`),
        this.cell(row.durationSec !== null ? `${row.durationSec.toFixed(1)}s` : "-"),
        this.cell(row.createdAt),
        this.playbackCell(row.playbackUrl),
        this.assetActionCell(row),
      );
      tbody.append(tr);
    }
    tableShell.append(table);

    const modal = document.createElement("portal-modal");
    if (this.confirmDelete !== null) {
      modal.setAttribute("open", "");
    }
    modal.setAttribute("heading", "Delete asset?");
    modal.append(
      this.message("p", `Soft-delete asset ${this.confirmDelete?.id ?? ""}? Playback will stop.`),
      this.modalButton("Confirm delete", "danger", () => {
        if (this.confirmDelete !== null) {
          void this.softDelete(this.confirmDelete);
        }
      }),
      this.modalButton("Cancel", "ghost", () => {
        this.confirmDelete = null;
        this.render();
      }),
    );

    card.append(tableShell, modal);
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

  private playbackCell(url: string | null): HTMLTableCellElement {
    const td = document.createElement("td");
    if (url === null) {
      td.textContent = "-";
      return td;
    }
    const link = document.createElement("a");
    link.href = url;
    link.textContent = "play";
    td.append(link);
    return td;
  }

  private assetActionCell(row: AssetRow): HTMLTableCellElement {
    const td = document.createElement("td");
    const button = document.createElement("button");
    button.className = "video-portal-page-button";
    if (row.deletedAt !== null) {
      button.textContent = "Restore";
      button.addEventListener("click", () => {
        void this.restore(row);
      });
    } else {
      button.textContent = "Delete";
      button.addEventListener("click", () => {
        this.confirmDelete = row;
        this.render();
      });
    }
    td.append(button);
    return td;
  }

  private modalButton(label: string, variant: string, onClick: () => void): HTMLElement {
    const button = document.createElement("portal-button");
    button.setAttribute("variant", variant);
    button.textContent = label;
    button.addEventListener("click", onClick);
    return button;
  }
}

if (!customElements.get("portal-assets")) {
  customElements.define("portal-assets", PortalAssets);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-assets": PortalAssets;
  }
}
