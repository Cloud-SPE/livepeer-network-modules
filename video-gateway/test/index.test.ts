import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { basename } from "node:path";
import { test } from "node:test";

import {
  resolveCustomerPortalMigrationsDir,
  resolveVideoGatewayMigrationsDir,
} from "../src/index.js";

test("resolveCustomerPortalMigrationsDir finds the shared portal migrations", () => {
  const dir = resolveCustomerPortalMigrationsDir();
  assert.ok(existsSync(dir));
  assert.equal(basename(dir), "migrations");
});

test("resolveVideoGatewayMigrationsDir finds the local gateway migrations", () => {
  const dir = resolveVideoGatewayMigrationsDir();
  assert.ok(existsSync(dir));
  assert.equal(basename(dir), "migrations");
});
