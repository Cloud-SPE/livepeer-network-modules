import test from 'node:test';
import assert from 'node:assert/strict';

import { createCustomerPortal, auth, billing, middleware, admin, db } from '@livepeer-network-modules/customer-portal';

test('customer-portal entrypoint exports createCustomerPortal', () => {
  assert.equal(typeof createCustomerPortal, 'function');
});

test('customer-portal exposes the namespaced module surfaces', () => {
  assert.equal(typeof auth, 'object');
  assert.equal(typeof billing, 'object');
  assert.equal(typeof middleware, 'object');
  assert.equal(typeof admin, 'object');
  assert.equal(typeof db, 'object');
});
