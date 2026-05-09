import { test, before } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

before(() => {
  const dom = new JSDOM('<!DOCTYPE html><html><body></body></html>', {
    url: 'http://localhost/',
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
  g.SubmitEvent = dom.window.SubmitEvent;
  g.HTMLFormElement = dom.window.HTMLFormElement;
  g.HTMLInputElement = dom.window.HTMLInputElement;
  g.requestAnimationFrame = (cb: FrameRequestCallback) => setTimeout(() => cb(performance.now()), 0);
  g.cancelAnimationFrame = (id: number) => clearTimeout(id);
});

test('shared widgets register custom elements', async () => {
  await import('../src/index.js');
  assert.ok(customElements.get('portal-button'));
  assert.ok(customElements.get('portal-input'));
  assert.ok(customElements.get('portal-card'));
  assert.ok(customElements.get('portal-action-row'));
  assert.ok(customElements.get('portal-data-table'));
  assert.ok(customElements.get('portal-detail-section'));
  assert.ok(customElements.get('portal-toast'));
  assert.ok(customElements.get('portal-modal'));
  assert.ok(customElements.get('portal-layout'));
  assert.ok(customElements.get('portal-balance'));
  assert.ok(customElements.get('portal-metric-tile'));
  assert.ok(customElements.get('portal-signup'));
  assert.ok(customElements.get('portal-login'));
  assert.ok(customElements.get('portal-api-keys'));
  assert.ok(customElements.get('portal-checkout-button'));
  assert.ok(customElements.get('portal-status-pill'));
});

test('portal-button renders with default variant', async () => {
  await import('../src/index.js');
  document.body.innerHTML = '<portal-button>Click</portal-button>';
  const el = document.querySelector('portal-button');
  assert.ok(el);
  assert.equal((el as any).variant, 'primary');
  assert.equal(el.querySelector('button')?.textContent?.trim(), 'Click');
});

test('portal-balance formats cents correctly', async () => {
  await import('../src/index.js');
  document.body.innerHTML =
    '<portal-balance currency="USD" balanceCents="12345" reservedCents="2300"></portal-balance>';
  const el = document.querySelector('portal-balance')!;
  const text = el.textContent ?? '';
  assert.ok(text.includes('$'));
});

test('portal-status-pill reflects the variant attribute', async () => {
  await import('../src/index.js');
  document.body.innerHTML = '<portal-status-pill variant="success">Live</portal-status-pill>';
  const el = document.querySelector('portal-status-pill')!;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  await (el as any).updateComplete;
  assert.equal(el.getAttribute('variant'), 'success');
});
