import { test } from "node:test";
import assert from "node:assert/strict";

import { createPaymentBuilder } from "../../src/livepeer/payment.js";
import {
  createUnixSocketPayerDaemonClient,
  type PayerDaemonClient,
} from "../../src/livepeer/payerDaemonClient.js";

function fakeClient(canned: { paymentHeader: string; payerWorkId: string }): PayerDaemonClient {
  return {
    async createPayment(_req) {
      return { paymentHeader: canned.paymentHeader, payerWorkId: canned.payerWorkId };
    },
    async close() {},
  };
}

test("buildPayment: forwards createPayment and returns header+workId", async () => {
  const builder = createPaymentBuilder({
    payerDaemon: fakeClient({ paymentHeader: "lp-payment-bytes-abc", payerWorkId: "work_1" }),
  });
  const out = await builder({
    callerId: "cust_1",
    capability: "video:transcode.abr",
    offering: "abr-default",
    workUnits: 60n,
    faceValueWei: "1000000000",
    recipientEthAddress: "0xfeedbeef",
    nodeId: "orch_1",
  });
  assert.equal(out.header, "lp-payment-bytes-abc");
  assert.equal(out.workId, "work_1");
});

test("buildPayment: surfaces createPayment errors", async () => {
  const builder = createPaymentBuilder({
    payerDaemon: {
      async createPayment() {
        throw new Error("daemon unreachable");
      },
      async close() {},
    },
  });
  await assert.rejects(
    () =>
      builder({
        callerId: "x",
        capability: "video:live.rtmp",
        offering: "live-1080p",
        workUnits: 1n,
        faceValueWei: "1",
        recipientEthAddress: "0x",
        nodeId: "n1",
      }),
    /daemon unreachable/,
  );
});

test("createUnixSocketPayerDaemonClient: forwards request body shape", async () => {
  const captured: { url?: string; body?: unknown } = {};
  const client = createUnixSocketPayerDaemonClient({
    socketPath: "/var/run/livepeer/payer.sock",
    fetchImpl: async (url, init) => {
      captured.url = url;
      captured.body = JSON.parse(String(init.body));
      return new Response(
        JSON.stringify({
          payer_work_id: "wk_X",
          payment_header: "lp-bytes-Y",
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    },
  });
  const r = await client.createPayment({
    faceValueWei: "1000",
    recipientEthAddress: "0xabc",
    capability: "video:transcode.abr",
    offering: "abr-default",
    nodeId: "orch_1",
  });
  assert.equal(r.payerWorkId, "wk_X");
  assert.equal(r.paymentHeader, "lp-bytes-Y");
  assert.deepEqual(captured.body, {
    face_value_wei: "1000",
    recipient_eth_address: "0xabc",
    capability: "video:transcode.abr",
    offering: "abr-default",
    node_id: "orch_1",
  });
});

test("createUnixSocketPayerDaemonClient: non-ok response throws", async () => {
  const client = createUnixSocketPayerDaemonClient({
    socketPath: "/x.sock",
    fetchImpl: async () => new Response(null, { status: 503 }),
  });
  await assert.rejects(
    () =>
      client.createPayment({
        faceValueWei: "1",
        recipientEthAddress: "0x",
        capability: "c",
        offering: "o",
        nodeId: "n",
      }),
    /503/,
  );
});
