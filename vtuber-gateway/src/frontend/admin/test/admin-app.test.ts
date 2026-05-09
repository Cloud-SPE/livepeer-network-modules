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
    new Response('{"customers":[],"sessions":[],"usage":[],"nodes":[],"rate_card":[],"events":[]}', { status: 200 })) as typeof fetch;
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

test("vtuber admin registers custom element", async () => {
  await import("../src/main.js");
  assert.ok(customElements.get("vtuber-gateway-admin"));
});

test("vtuber admin navigates to sessions route", async () => {
  await import("../src/main.js");
  seedSession();
  document.body.innerHTML = "<vtuber-gateway-admin></vtuber-gateway-admin>";
  window.location.hash = "#/sessions";
  const el = document.querySelector("vtuber-gateway-admin")! as unknown as Element;
  await settle();
  assert.ok((el.textContent as string).includes("Sessions"));
});
