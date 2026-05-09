import { before, test } from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";

const SESSION_KEY = "customer-portal:session";

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
  g.sessionStorage = dom.window.sessionStorage;
  g.customElements = dom.window.customElements;
  g.Node = dom.window.Node;
  g.Event = dom.window.Event;
  g.CustomEvent = dom.window.CustomEvent;
  g.requestAnimationFrame = (cb: FrameRequestCallback): number =>
    setTimeout(() => cb(performance.now()), 0) as unknown as number;
  g.cancelAnimationFrame = (id: number): void => clearTimeout(id);
});

function seedSession(): void {
  window.sessionStorage.setItem(
    SESSION_KEY,
    JSON.stringify({ token: "customer-token", actor: "customer" }),
  );
}

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await new Promise((resolve) => setTimeout(resolve, 0));
}

test("vtuber portal SPA registers all custom elements", async () => {
  await import("../src/main.js");
  assert.ok(customElements.get("vtuber-gateway-portal"));
  assert.ok(customElements.get("portal-vtuber-sessions"));
  assert.ok(customElements.get("portal-vtuber-persona"));
  assert.ok(customElements.get("portal-vtuber-history"));
});

test("vtuber portal default route renders session page", async () => {
  await import("../src/main.js");
  seedSession();
  document.body.innerHTML = "<vtuber-gateway-portal></vtuber-gateway-portal>";
  window.location.hash = "#/sessions";
  window.dispatchEvent(new Event("hashchange"));
  const el = document.querySelector("vtuber-gateway-portal")!;
  await settle();
  assert.ok(el.querySelector("portal-vtuber-sessions"));
});
