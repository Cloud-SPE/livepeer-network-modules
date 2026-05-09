import { ApiClient } from "@livepeer-rewrite/customer-portal-shared";

interface StreamRow {
  id: string;
  name: string;
  recordToVod: boolean;
}

interface RecordingRow {
  id: string;
  streamId: string;
  streamName: string;
  assetId: string | null;
  status: string;
  durationSec: number | null;
  startedAt: string;
  endedAt: string | null;
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

export class PortalRecordings extends HTMLElement {
  private streams: StreamRow[] = [];
  private recordings: RecordingRow[] = [];
  private error: string | null = null;
  private busy: Record<string, boolean> = {};

  private readonly api = new ApiClient({ baseUrl: "" });

  connectedCallback(): void {
    installStyles();
    void this.load();
  }

  private async load(): Promise<void> {
    this.error = null;
    try {
      const streams = await this.api.get<{ items: StreamRow[] }>("/portal/live-streams");
      this.streams = streams.items ?? [];
      const recordings = await this.api.get<{ items: RecordingRow[] }>("/portal/recordings");
      this.recordings = recordings.items ?? [];
    } catch (err) {
      this.error = err instanceof Error ? err.message : "load_failed";
    }
    this.render();
  }

  private async toggleRecord(row: StreamRow, checked: boolean): Promise<void> {
    this.busy = { ...this.busy, [row.id]: true };
    this.render();
    try {
      await this.api.post(`/portal/live-streams/${encodeURIComponent(row.id)}/record`, {
        record_to_vod: checked,
      });
      await this.load();
    } catch (err) {
      this.error = err instanceof Error ? err.message : "toggle_failed";
      this.render();
    } finally {
      const next = { ...this.busy };
      delete next[row.id];
      this.busy = next;
      this.render();
    }
  }

  private render(): void {
    const card = document.createElement("portal-card");
    card.setAttribute("heading", "Recordings");
    if (this.error !== null) {
      card.append(this.message("p", this.error, "video-portal-page-error"));
    }
    card.append(
      this.message(
        "p",
        "Recording is opt-in per stream. New streams default to OFF; toggle below to enable VOD capture.",
        "video-portal-page-note",
      ),
      this.policyTable(),
      this.recordingsTable(),
    );
    this.replaceChildren(card);
  }

  private policyTable(): HTMLElement {
    const section = document.createElement("section");
    const shell = document.createElement("portal-data-table");
    shell.setAttribute("heading", "Per-Stream Recording Policy");
    shell.setAttribute("description", "Turn recording on for new or existing live streams before they begin broadcasting.");
    const table = document.createElement("table");
    table.className = "video-portal-page-table";
    table.innerHTML = `
      <thead>
        <tr><th>Stream</th><th>Record</th></tr>
      </thead>
      <tbody></tbody>
    `;
    const tbody = table.tBodies[0]!;
    for (const stream of this.streams) {
      const tr = document.createElement("tr");
      const nameCell = document.createElement("td");
      nameCell.textContent = stream.name;
      const toggleCell = document.createElement("td");
      const input = document.createElement("input");
      input.type = "checkbox";
      input.checked = stream.recordToVod;
      input.disabled = !!this.busy[stream.id];
      input.addEventListener("change", (event) => {
        void this.toggleRecord(stream, (event.target as HTMLInputElement).checked);
      });
      toggleCell.append(input);
      tr.append(nameCell, toggleCell);
      tbody.append(tr);
    }
    shell.append(table);
    section.append(shell);
    return section;
  }

  private recordingsTable(): HTMLElement {
    const section = document.createElement("section");
    const shell = document.createElement("portal-data-table");
    shell.setAttribute("heading", "Recorded Sessions");
    shell.setAttribute("description", "Completed and in-progress VOD captures generated from your live streams.");
    const table = document.createElement("table");
    table.className = "video-portal-page-table";
    table.innerHTML = `
      <thead>
        <tr><th>Stream</th><th>Status</th><th>Asset</th><th>Duration</th><th>Started</th><th>Ended</th></tr>
      </thead>
      <tbody></tbody>
    `;
    const tbody = table.tBodies[0]!;
    for (const recording of this.recordings) {
      const tr = document.createElement("tr");
      tr.append(
        this.cell(recording.streamName),
        this.statusCell(
          recording.status,
          recording.status === "ready" ? "success" : recording.status === "failed" ? "danger" : "info",
        ),
        this.assetCell(recording.assetId),
        this.cell(recording.durationSec !== null ? `${recording.durationSec.toFixed(1)}s` : "-"),
        this.cell(recording.startedAt),
        this.maybeDimCell(recording.endedAt, "active"),
      );
      tbody.append(tr);
    }
    shell.append(table);
    section.append(shell);
    return section;
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

  private statusCell(text: string, variant: string): HTMLTableCellElement {
    const td = document.createElement("td");
    const pill = document.createElement("portal-status-pill");
    pill.setAttribute("variant", variant);
    pill.textContent = text;
    td.append(pill);
    return td;
  }

  private assetCell(assetId: string | null): HTMLTableCellElement {
    const td = document.createElement("td");
    if (assetId === null) {
      td.append(this.message("span", "-", "video-portal-page-dim"));
      return td;
    }
    const link = document.createElement("a");
    link.href = "#/assets";
    link.textContent = assetId;
    td.append(link);
    return td;
  }

  private maybeDimCell(value: string | null, fallback: string): HTMLTableCellElement {
    const td = document.createElement("td");
    if (value !== null) {
      td.textContent = value;
      return td;
    }
    td.append(this.message("span", fallback, "video-portal-page-dim"));
    return td;
  }
}

if (!customElements.get("portal-recordings")) {
  customElements.define("portal-recordings", PortalRecordings);
}

declare global {
  interface HTMLElementTagNameMap {
    "portal-recordings": PortalRecordings;
  }
}
