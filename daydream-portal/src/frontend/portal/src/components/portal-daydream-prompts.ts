// Personal saved-prompts library. Pure user-owned: no sharing, no
// community surface. Survives item-2 strip because it's strictly a
// per-customer scratchpad.

import { DaydreamPortalApi, type SavedPrompt } from "../lib/api.js";

export class PortalDaydreamPrompts extends HTMLElement {
  private api = new DaydreamPortalApi();
  private prompts: SavedPrompt[] = [];
  private loading = true;
  private error: string | null = null;
  private editing: { id: string | "new"; label: string; body: string } | null =
    null;

  connectedCallback(): void {
    this.render();
    void this.load();
  }

  private async load(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await this.api.listPrompts();
      this.prompts = res.prompts;
    } catch (err) {
      this.error = `Could not load prompts: ${err instanceof Error ? err.message : String(err)}`;
    } finally {
      this.loading = false;
      this.render();
    }
  }

  private render(): void {
    if (this.editing) {
      const isNew = this.editing.id === "new";
      this.innerHTML = `
        <portal-card heading="${isNew ? "New prompt" : "Edit prompt"}">
          <portal-input id="dd-prompt-label" label="Label" value="${escapeAttr(this.editing.label)}"></portal-input>
          <label class="portal-form-label">Body
            <textarea id="dd-prompt-body" rows="6"
              style="width:100%;font-family:monospace">${escapeHtml(this.editing.body)}</textarea>
          </label>
          ${this.error ? `<p class="portal-form-error">${escapeHtml(this.error)}</p>` : ""}
          <portal-action-row>
            <portal-button id="dd-prompt-save">Save</portal-button>
            <portal-button id="dd-prompt-cancel" variant="ghost">Cancel</portal-button>
          </portal-action-row>
        </portal-card>`;
      this.querySelector("#dd-prompt-save")?.addEventListener("click", () => {
        void this.save();
      });
      this.querySelector("#dd-prompt-cancel")?.addEventListener("click", () => {
        this.editing = null;
        this.render();
      });
      return;
    }

    this.innerHTML = `
      <portal-card heading="Saved prompts">
        <portal-action-row>
          <portal-button id="dd-prompt-new">New prompt</portal-button>
        </portal-action-row>
        ${this.loading ? "<p>Loading…</p>" : ""}
        ${this.error ? `<p class="portal-form-error">${escapeHtml(this.error)}</p>` : ""}
        ${
          this.prompts.length === 0 && !this.loading
            ? "<p>No saved prompts yet.</p>"
            : ""
        }
        <ul class="portal-daydream-prompts__list" style="list-style:none;padding:0;margin:0">
          ${this.prompts.map((p) => this.renderRow(p)).join("")}
        </ul>
      </portal-card>`;
    this.querySelector("#dd-prompt-new")?.addEventListener("click", () => {
      this.editing = { id: "new", label: "", body: "" };
      this.render();
    });
    this.prompts.forEach((p) => {
      this.querySelector(`[data-action="edit"][data-id="${p.id}"]`)?.addEventListener(
        "click",
        () => {
          this.editing = { id: p.id, label: p.label, body: p.body };
          this.render();
        },
      );
      this.querySelector(`[data-action="delete"][data-id="${p.id}"]`)?.addEventListener(
        "click",
        () => {
          void this.deleteOne(p.id);
        },
      );
      this.querySelector(`[data-action="copy"][data-id="${p.id}"]`)?.addEventListener(
        "click",
        () => {
          void navigator.clipboard.writeText(p.body);
        },
      );
    });
  }

  private renderRow(p: SavedPrompt): string {
    return `
      <li style="padding:.5rem 0;border-bottom:1px solid var(--portal-border,#eee)">
        <div style="display:flex;justify-content:space-between;align-items:center">
          <strong>${escapeHtml(p.label)}</strong>
          <span>
            <portal-button data-action="copy" data-id="${p.id}" variant="ghost">Copy</portal-button>
            <portal-button data-action="edit" data-id="${p.id}" variant="ghost">Edit</portal-button>
            <portal-button data-action="delete" data-id="${p.id}" variant="danger">Delete</portal-button>
          </span>
        </div>
        <pre style="white-space:pre-wrap;margin:.25rem 0 0;font-size:.9em">${escapeHtml(p.body)}</pre>
      </li>`;
  }

  private async save(): Promise<void> {
    if (!this.editing) return;
    const label = (this.querySelector<HTMLInputElement>("#dd-prompt-label")
      ?.value ?? "").trim();
    const body = (this.querySelector<HTMLTextAreaElement>("#dd-prompt-body")
      ?.value ?? "").trim();
    if (!label || !body) {
      this.error = "Both label and body are required.";
      this.render();
      return;
    }
    this.error = null;
    try {
      if (this.editing.id === "new") {
        await this.api.createPrompt({ label, body });
      } else {
        await this.api.updatePrompt(this.editing.id, { label, body });
      }
      this.editing = null;
      await this.load();
    } catch (err) {
      this.error = `Save failed: ${err instanceof Error ? err.message : String(err)}`;
      this.render();
    }
  }

  private async deleteOne(id: string): Promise<void> {
    if (!window.confirm("Delete this prompt?")) return;
    try {
      await this.api.deletePrompt(id);
      await this.load();
    } catch (err) {
      this.error = `Delete failed: ${err instanceof Error ? err.message : String(err)}`;
      this.render();
    }
  }
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function escapeAttr(s: string): string {
  return escapeHtml(s).replace(/'/g, "&#39;");
}

customElements.define("portal-daydream-prompts", PortalDaydreamPrompts);
