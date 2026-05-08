import assert from "node:assert/strict";
import test from "node:test";
import { createServer } from "node:http";

import { WebSocketServer } from "ws";

import { createBrokerWorkerClient } from "../../src/providers/workerClient.js";

test("topupSession sends session.topup with payment header over control websocket", async () => {
  let receivedPath = "";
  let receivedPayload: Record<string, unknown> | null = null;

  const server = createServer();
  const wss = new WebSocketServer({ server });
  wss.on("connection", (socket, req) => {
    receivedPath = req.url ?? "";
    socket.once("message", (data) => {
      receivedPayload = JSON.parse(String(data)) as Record<string, unknown>;
      socket.close();
    });
  });

  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", () => resolve()));
  const address = server.address();
  assert.ok(address && typeof address === "object");

  try {
    const client = createBrokerWorkerClient();
    await client.topupSession(`http://127.0.0.1:${address.port}`, {
      sessionId: "sess_test123",
      paymentHeader: "cGF5bWVudA==",
    });
  } finally {
    await new Promise<void>((resolve, reject) =>
      wss.close((err) => (err ? reject(err) : resolve())),
    );
    await new Promise<void>((resolve, reject) =>
      server.close((err) => (err ? reject(err) : resolve())),
    );
  }

  assert.equal(receivedPath, "/v1/cap/sess_test123/control");
  assert.deepEqual(receivedPayload, {
    type: "session.topup",
    body: {
      payment_header: "cGF5bWVudA==",
    },
  });
});
