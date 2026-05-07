import test from "node:test";
import assert from "node:assert/strict";

import {
  createReconnectWindow,
  createReplayBuffer,
  parseLastSeqHeader,
} from "../../src/service/relay/reconnectWindow.js";

const CFG = {
  windowMs: 1_000,
  bufferMessages: 64,
  bufferBytes: 1 << 20,
};

interface FakeClock {
  now(): number;
  schedule(fn: () => void, ms: number): NodeJS.Timeout;
  cancel(handle: NodeJS.Timeout): void;
  fire(): void;
  current: number;
  pending: Array<{ id: number; fn: () => void; due: number }>;
}

function fakeClock(): FakeClock {
  let cur = 1_700_000_000_000;
  let nextId = 1;
  const pending: Array<{ id: number; fn: () => void; due: number }> = [];
  return {
    pending,
    get current() {
      return cur;
    },
    set current(v: number) {
      cur = v;
    },
    now() {
      return cur;
    },
    schedule(fn, ms) {
      const id = nextId++;
      pending.push({ id, fn, due: cur + ms });
      return id as unknown as NodeJS.Timeout;
    },
    cancel(handle) {
      const idx = pending.findIndex((p) => (p.id as unknown) === handle);
      if (idx >= 0) {
        pending.splice(idx, 1);
      }
    },
    fire() {
      const due = pending.shift();
      if (due !== undefined) {
        cur = due.due;
        due.fn();
      }
    },
  };
}

test("replayBuffer drops oldest entries when message-cap is hit", () => {
  const buf = createReplayBuffer(3, 1 << 20);
  buf.append(1, "a");
  buf.append(2, "b");
  buf.append(3, "c");
  buf.append(4, "d");
  assert.equal(buf.size(), 3);
  const out = buf.since(0).map((e) => e.seq);
  assert.deepEqual(out, [2, 3, 4]);
});

test("replayBuffer drops oldest entries when byte-cap is hit", () => {
  const buf = createReplayBuffer(64, 8);
  buf.append(1, "1234"); // 4 bytes
  buf.append(2, "5678"); // 4 bytes (total 8)
  buf.append(3, "abcd"); // pushes total to 12 → evict
  assert.ok(buf.bytes() <= 8);
  const out = buf.since(0).map((e) => e.seq);
  assert.deepEqual(out, [2, 3]);
});

test("replayBuffer.since returns only entries with seq > lastSeq", () => {
  const buf = createReplayBuffer(64, 1 << 20);
  buf.append(5, "five");
  buf.append(6, "six");
  buf.append(7, "seven");
  const out = buf.since(5).map((e) => e.payload);
  assert.deepEqual(out, ["six", "seven"]);
});

test("attachCustomer-then-detach holds session for the reconnect window", () => {
  const clock = fakeClock();
  let expired: string | null = null;
  const w = createReconnectWindow({
    cfg: CFG,
    onWindowExpiry: (id) => {
      expired = id;
    },
    now: clock.now.bind(clock),
    schedule: clock.schedule.bind(clock),
    cancel: clock.cancel.bind(clock),
  });
  const sess = w.registerSession("s1");
  const r1 = w.attachCustomer("s1", 0);
  assert.equal(r1.kind, "attached");
  assert.equal(sess.active, true);

  w.recordOutbound("s1", "m1");
  w.recordOutbound("s1", "m2");
  w.detachCustomer("s1");
  assert.equal(sess.active, false);

  const r2 = w.attachCustomer("s1", 1);
  assert.equal(r2.kind, "attached");
  if (r2.kind === "attached") {
    assert.deepEqual(
      r2.replay.map((e) => e.payload),
      ["m2"],
    );
  }
  assert.equal(expired, null);
});

test("window expiry fires onWindowExpiry and removes the session", () => {
  const clock = fakeClock();
  let expired: string | null = null;
  const w = createReconnectWindow({
    cfg: CFG,
    onWindowExpiry: (id) => {
      expired = id;
    },
    now: clock.now.bind(clock),
    schedule: clock.schedule.bind(clock),
    cancel: clock.cancel.bind(clock),
  });
  w.registerSession("s2");
  w.attachCustomer("s2", 0);
  w.recordOutbound("s2", "p1");
  w.detachCustomer("s2");

  clock.fire();

  assert.equal(expired, "s2");
  assert.equal(w.hasSession("s2"), false);
});

test("attaching twice without dropping is rejected as a conflict", () => {
  const clock = fakeClock();
  const w = createReconnectWindow({
    cfg: CFG,
    onWindowExpiry: () => {},
    now: clock.now.bind(clock),
    schedule: clock.schedule.bind(clock),
    cancel: clock.cancel.bind(clock),
  });
  w.registerSession("race");
  const r1 = w.attachCustomer("race", 0);
  assert.equal(r1.kind, "attached");
  const r2 = w.attachCustomer("race", 0);
  assert.equal(r2.kind, "conflict");
  if (r2.kind === "conflict") {
    assert.equal(r2.reason, "already_attached");
  }
});

test("recordOutbound increments seq monotonically and survives reconnect", () => {
  const clock = fakeClock();
  const w = createReconnectWindow({
    cfg: CFG,
    onWindowExpiry: () => {},
    now: clock.now.bind(clock),
    schedule: clock.schedule.bind(clock),
    cancel: clock.cancel.bind(clock),
  });
  w.registerSession("seq-test");
  w.attachCustomer("seq-test", 0);
  assert.equal(w.recordOutbound("seq-test", "x"), 1);
  assert.equal(w.recordOutbound("seq-test", "y"), 2);
  w.detachCustomer("seq-test");
  const r = w.attachCustomer("seq-test", 1);
  if (r.kind === "attached") {
    assert.equal(r.nextSeq, 2);
    assert.deepEqual(
      r.replay.map((e) => e.seq),
      [2],
    );
  }
});

test("endSession drops the entry and cancels the expiry timer", () => {
  const clock = fakeClock();
  let expired: string | null = null;
  const w = createReconnectWindow({
    cfg: CFG,
    onWindowExpiry: (id) => {
      expired = id;
    },
    now: clock.now.bind(clock),
    schedule: clock.schedule.bind(clock),
    cancel: clock.cancel.bind(clock),
  });
  w.registerSession("kill");
  w.attachCustomer("kill", 0);
  w.detachCustomer("kill");
  assert.equal(clock.pending.length, 1);
  w.endSession("kill");
  assert.equal(clock.pending.length, 0);
  assert.equal(w.hasSession("kill"), false);
  assert.equal(expired, null);
});

test("parseLastSeqHeader handles strings, missing, and non-numeric", () => {
  assert.equal(parseLastSeqHeader("17"), 17);
  assert.equal(parseLastSeqHeader(undefined), 0);
  assert.equal(parseLastSeqHeader(""), 0);
  assert.equal(parseLastSeqHeader("not-a-number"), 0);
  assert.equal(parseLastSeqHeader("-5"), 0);
});
