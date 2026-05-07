import test from "node:test";
import assert from "node:assert/strict";

import {
  hashSessionBearer,
  mintSessionBearer,
} from "../../src/service/auth/sessionBearer.js";
import {
  hashWorkerControlBearer,
  mintWorkerControlBearer,
} from "../../src/service/auth/workerControlBearer.js";

const PEPPER = "pepper-min-16-chars-test-only";

test("mintSessionBearer emits vtbs_<43-char> and a stable hash", () => {
  const { bearer, hash } = mintSessionBearer(PEPPER);
  assert.match(bearer, /^vtbs_[A-Za-z0-9_-]{43}$/);
  assert.equal(hashSessionBearer(bearer, PEPPER), hash);
});

test("hashSessionBearer rejects malformed bearers", () => {
  assert.throws(() => hashSessionBearer("not-a-bearer", PEPPER));
  assert.throws(() => hashSessionBearer("vtbs_short", PEPPER));
});

test("mintSessionBearer rejects short pepper", () => {
  assert.throws(() => mintSessionBearer("short"));
});

test("session-bearer hash differs across peppers", () => {
  const a = mintSessionBearer(PEPPER);
  const otherPepper = "different-pepper-min-16-chars";
  const reHashed = hashSessionBearer(a.bearer, otherPepper);
  assert.notEqual(reHashed, a.hash);
});

test("mintWorkerControlBearer emits vtbsw_<43-char> and a stable hash", () => {
  const { bearer, hash } = mintWorkerControlBearer(PEPPER);
  assert.match(bearer, /^vtbsw_[A-Za-z0-9_-]{43}$/);
  assert.equal(hashWorkerControlBearer(bearer, PEPPER), hash);
});

test("hashWorkerControlBearer rejects bearers with the wrong prefix", () => {
  const { bearer } = mintSessionBearer(PEPPER);
  assert.throws(() => hashWorkerControlBearer(bearer, PEPPER));
});
