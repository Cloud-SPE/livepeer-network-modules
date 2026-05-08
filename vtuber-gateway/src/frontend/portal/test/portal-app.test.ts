import { before, test } from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";

before(() => {
  const dom = new JSDOM("<!DOCTYPE html><html><body></body></html>", {
    url: "http://localhost/",
    pretendToBeVisual: true,
  });
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const g = globalThis as any;
  g.window = dom.window;
  g.document = dom.window.document;
  g.HTMLElement = dom.window.HTMLElement;
  g.customElements = dom.window.customElements;
  g.Node = dom.window.Node;
  g.Event = dom.window.Event;
  g.CustomEvent = dom.window.CustomEvent;
  g.requestAnimationFrame = (cb: FrameRequestCallback): number =>
    setTimeout(() => cb(performance.now()), 0) as unknown as number;
  g.cancelAnimationFrame = (id: number): void => clearTimeout(id);
});

test("vtuber portal SPA registers all custom elements", async () => {
  await import("../src/main.js");
  assert.ok(customElements.get("vtuber-gateway-portal"));
  assert.ok(customElements.get("portal-vtuber-sessions"));
  assert.ok(customElements.get("portal-vtuber-persona"));
  assert.ok(customElements.get("portal-vtuber-history"));
});

test("vtuber portal default route renders session page", async () => {
  await import("../src/main.js");
  document.body.innerHTML = "<vtuber-gateway-portal></vtuber-gateway-portal>";
  window.location.hash = "#/sessions";
  window.dispatchEvent(new Event("hashchange"));
  const el = document.querySelector("vtuber-gateway-portal")!;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  await (el as any).updateComplete;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const root = (el as any).shadowRoot!;
  assert.ok((root.innerHTML as string).includes("portal-vtuber-sessions"));
});
