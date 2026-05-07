import { test, before } from "node:test";
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
  g.HTMLInputElement = dom.window.HTMLInputElement;
  g.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  g.HTMLFormElement = dom.window.HTMLFormElement;
  g.customElements = dom.window.customElements;
  g.Node = dom.window.Node;
  g.Event = dom.window.Event;
  g.CustomEvent = dom.window.CustomEvent;
  g.SubmitEvent = dom.window.SubmitEvent;
  g.requestAnimationFrame = (cb: FrameRequestCallback): number =>
    setTimeout(() => cb(performance.now()), 0) as unknown as number;
  g.cancelAnimationFrame = (id: number): void => clearTimeout(id);
  g.fetch = (async (): Promise<Response> =>
    new Response('{"items":[]}', { status: 200 })) as typeof fetch;
});

test("admin SPA registers all custom elements", async () => {
  await import("../web-ui/main.js");
  assert.ok(customElements.get("video-gateway-admin"));
  assert.ok(customElements.get("admin-customers"));
  assert.ok(customElements.get("admin-customer-detail"));
  assert.ok(customElements.get("admin-customer-adjust"));
  assert.ok(customElements.get("admin-customer-refund"));
  assert.ok(customElements.get("admin-assets"));
  assert.ok(customElements.get("admin-streams"));
  assert.ok(customElements.get("admin-webhooks"));
  assert.ok(customElements.get("admin-recordings"));
});

test("admin-app routes resolve via hash", async () => {
  await import("../web-ui/main.js");
  document.body.innerHTML = "<video-gateway-admin></video-gateway-admin>";
  window.location.hash = "#/customers";
  const el = document.querySelector("video-gateway-admin")!;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  await (el as any).updateComplete;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const root = (el as any).shadowRoot!;
  const html = root.innerHTML as string;
  assert.ok(html.includes("Customers"));
  assert.ok(html.includes("admin-customers"));
});

test("admin-app navigates to assets route", async () => {
  await import("../web-ui/main.js");
  document.body.innerHTML = "<video-gateway-admin></video-gateway-admin>";
  window.location.hash = "#/assets";
  window.dispatchEvent(new Event("hashchange"));
  const el = document.querySelector("video-gateway-admin")!;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  await (el as any).updateComplete;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const root = (el as any).shadowRoot!;
  assert.ok((root.innerHTML as string).includes("admin-assets"));
});
