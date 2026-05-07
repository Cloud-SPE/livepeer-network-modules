import test from "node:test";
import assert from "node:assert/strict";

import {
  createSessionRelay,
  type SessionConnections,
} from "../../src/service/relay/sessionRelay.js";

const SESSION_ID = "00000000-0000-0000-0000-000000000001";
const CUSTOMER_ID = "00000000-0000-0000-0000-000000000099";

interface FakeSocket {
  closed: boolean;
  closeArgs: [number, string] | null;
  readyState: number;
  OPEN: number;
  close(code: number, reason: string): void;
  send(_payload: string | Buffer): void;
}

function fakeSocket(): FakeSocket {
  return {
    closed: false,
    closeArgs: null,
    readyState: 1,
    OPEN: 1,
    close(code: number, reason: string) {
      this.closed = true;
      this.closeArgs = [code, reason];
      this.readyState = 3;
    },
    send() {
      return;
    },
  };
}

test("attachWorker is idempotent and replaces a stale worker", () => {
  const relay = createSessionRelay();
  const w1 = fakeSocket();
  const w2 = fakeSocket();
  relay.attachWorker(SESSION_ID, CUSTOMER_ID, w1 as never);
  relay.attachWorker(SESSION_ID, CUSTOMER_ID, w2 as never);

  const inspected = relay.inspect(SESSION_ID) as SessionConnections;
  assert.equal(inspected.worker, w2 as unknown);
  assert.equal(w1.closed, true);
  assert.equal(w1.closeArgs?.[0], 1012);
});

test("attachCustomer accumulates customers; detach removes them", () => {
  const relay = createSessionRelay();
  const c1 = fakeSocket();
  const c2 = fakeSocket();
  relay.attachCustomer(SESSION_ID, CUSTOMER_ID, c1 as never);
  relay.attachCustomer(SESSION_ID, CUSTOMER_ID, c2 as never);
  assert.equal(relay.inspect(SESSION_ID)?.customers.size, 2);

  relay.detach(SESSION_ID, c1 as never);
  assert.equal(relay.inspect(SESSION_ID)?.customers.size, 1);
});

test("detach removes the entry once worker + customers are gone", () => {
  const relay = createSessionRelay();
  const w = fakeSocket();
  const c = fakeSocket();
  relay.attachWorker(SESSION_ID, CUSTOMER_ID, w as never);
  relay.attachCustomer(SESSION_ID, CUSTOMER_ID, c as never);

  relay.detach(SESSION_ID, w as never);
  relay.detach(SESSION_ID, c as never);

  assert.equal(relay.has(SESSION_ID), false);
  assert.equal(relay.size(), 0);
});

test("endAll closes every socket and drops the entry", () => {
  const relay = createSessionRelay();
  const w = fakeSocket();
  const c1 = fakeSocket();
  const c2 = fakeSocket();
  relay.attachWorker(SESSION_ID, CUSTOMER_ID, w as never);
  relay.attachCustomer(SESSION_ID, CUSTOMER_ID, c1 as never);
  relay.attachCustomer(SESSION_ID, CUSTOMER_ID, c2 as never);

  relay.endAll(SESSION_ID);

  assert.equal(w.closed, true);
  assert.equal(c1.closed, true);
  assert.equal(c2.closed, true);
  assert.equal(relay.has(SESSION_ID), false);
});
