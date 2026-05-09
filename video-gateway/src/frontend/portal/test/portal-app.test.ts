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
    new Response('{"items":[]}', { status: 200 })) as typeof fetch;
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

test("portal SPA registers all custom elements", async () => {
  await import("../src/main.js");
  assert.ok(customElements.get("video-gateway-portal"));
  assert.ok(customElements.get("portal-assets"));
  assert.ok(customElements.get("portal-streams"));
  assert.ok(customElements.get("portal-webhooks"));
  assert.ok(customElements.get("portal-recordings"));
});

test("portal-app /assets route renders portal-assets", async () => {
  await import("../src/main.js");
  seedSession();
  document.body.innerHTML = "<video-gateway-portal></video-gateway-portal>";
  window.location.hash = "#/assets";
  window.dispatchEvent(new Event("hashchange"));
  const el = document.querySelector("video-gateway-portal")!;
  await settle();
  assert.ok(el.querySelector("portal-assets"));
});

test("portal-app /streams route renders portal-streams", async () => {
  await import("../src/main.js");
  seedSession();
  document.body.innerHTML = "<video-gateway-portal></video-gateway-portal>";
  window.location.hash = "#/streams";
  window.dispatchEvent(new Event("hashchange"));
  const el = document.querySelector("video-gateway-portal")!;
  await settle();
  assert.ok(el.querySelector("portal-streams"));
});

test("portal-app /webhooks route renders portal-webhooks", async () => {
  await import("../src/main.js");
  seedSession();
  document.body.innerHTML = "<video-gateway-portal></video-gateway-portal>";
  window.location.hash = "#/webhooks";
  window.dispatchEvent(new Event("hashchange"));
  const el = document.querySelector("video-gateway-portal")!;
  await settle();
  assert.ok(el.querySelector("portal-webhooks"));
});

test("portal-app /recordings route renders portal-recordings", async () => {
  await import("../src/main.js");
  seedSession();
  document.body.innerHTML = "<video-gateway-portal></video-gateway-portal>";
  window.location.hash = "#/recordings";
  window.dispatchEvent(new Event("hashchange"));
  const el = document.querySelector("video-gateway-portal")!;
  await settle();
  assert.ok(el.querySelector("portal-recordings"));
});

test("portal-app /api-keys route re-exports shared portal-api-keys", async () => {
  await import("../src/main.js");
  seedSession();
  document.body.innerHTML = "<video-gateway-portal></video-gateway-portal>";
  window.location.hash = "#/api-keys";
  window.dispatchEvent(new Event("hashchange"));
  const el = document.querySelector("video-gateway-portal")!;
  await settle();
  assert.ok(el.querySelector("portal-api-keys"));
});
