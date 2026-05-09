import { test, before } from "node:test";
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
  g.HTMLInputElement = dom.window.HTMLInputElement;
  g.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  g.HTMLFormElement = dom.window.HTMLFormElement;
  g.sessionStorage = dom.window.sessionStorage;
  g.customElements = dom.window.customElements;
  g.Node = dom.window.Node;
  g.Event = dom.window.Event;
  g.CustomEvent = dom.window.CustomEvent;
  g.SubmitEvent = dom.window.SubmitEvent;
  g.requestAnimationFrame = (cb: FrameRequestCallback): number =>
    setTimeout(() => cb(performance.now()), 0) as unknown as number;
  g.cancelAnimationFrame = (id: number): void => clearTimeout(id);
  g.fetch = (async (): Promise<Response> =>
    new Response('{"items":[],"customers":[],"auth_tokens":[],"api_keys":[],"topups":[],"reservations":[],"events":[]}', { status: 200 })) as typeof fetch;
});

function seedSession(): void {
  window.sessionStorage.setItem(
    SESSION_KEY,
    JSON.stringify({ token: "admin-token", actor: "operator" }),
  );
}

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await new Promise((resolve) => setTimeout(resolve, 0));
}

test("admin SPA registers all custom elements", async () => {
  await import("../web-ui/main.js");
  assert.ok(customElements.get("video-gateway-admin"));
  assert.ok(customElements.get("admin-customers"));
  assert.ok(customElements.get("admin-customer-detail"));
  assert.ok(customElements.get("admin-customer-adjust"));
  assert.ok(customElements.get("admin-customer-refund"));
  assert.ok(customElements.get("admin-health"));
  assert.ok(customElements.get("admin-assets"));
  assert.ok(customElements.get("admin-topups"));
  assert.ok(customElements.get("admin-reservations"));
  assert.ok(customElements.get("admin-audit"));
  assert.ok(customElements.get("admin-streams"));
  assert.ok(customElements.get("admin-webhooks"));
  assert.ok(customElements.get("admin-recordings"));
});

test("admin-app routes resolve via hash", async () => {
  await import("../web-ui/main.js");
  seedSession();
  document.body.innerHTML = "<video-gateway-admin></video-gateway-admin>";
  window.location.hash = "#/customers";
  const el = document.querySelector("video-gateway-admin")!;
  await settle();
  assert.ok((el.textContent as string).includes("Customers"));
  assert.ok(el.querySelector("admin-customers"));
});

test("admin-app navigates to assets route", async () => {
  await import("../web-ui/main.js");
  seedSession();
  document.body.innerHTML = "<video-gateway-admin></video-gateway-admin>";
  window.location.hash = "#/assets";
  window.dispatchEvent(new Event("hashchange"));
  const el = document.querySelector("video-gateway-admin")!;
  await settle();
  assert.ok(el.querySelector("admin-assets"));
});

test("admin-app navigates to reservations route", async () => {
  await import("../web-ui/main.js");
  seedSession();
  document.body.innerHTML = "<video-gateway-admin></video-gateway-admin>";
  window.location.hash = "#/reservations";
  window.dispatchEvent(new Event("hashchange"));
  const el = document.querySelector("video-gateway-admin")!;
  await settle();
  assert.ok(el.querySelector("admin-reservations"));
});
