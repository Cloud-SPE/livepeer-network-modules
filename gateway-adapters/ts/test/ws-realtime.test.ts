import { describe, it, before, after } from "node:test";
import assert from "node:assert/strict";
import * as http from "node:http";
import type { AddressInfo } from "node:net";

import { WebSocketServer, type WebSocket as ServerWebSocket } from "ws";

import { connect, MODE } from "../src/modes/ws-realtime.js";
import { HEADER, SPEC_VERSION } from "../src/headers.js";
import { LivepeerBrokerError } from "../src/errors.js";
import type { SessionDebitsClient } from "../src/payer-daemon.js";

interface UpgradeRecord {
  path: string | undefined;
  headers: http.IncomingHttpHeaders;
}

describe("ws-realtime middleware", () => {
  let server: http.Server;
  let wss: WebSocketServer;
  let port: number;
  let lastUpgrade: UpgradeRecord | undefined;
  let rejectNextUpgrade:
    | { status: number; headers: Record<string, string>; body: string }
    | undefined;

  before(async () => {
    server = http.createServer((_req, res) => {
      res.statusCode = 404;
      res.end();
    });

    wss = new WebSocketServer({ noServer: true });
    wss.on("connection", (sock: ServerWebSocket) => {
      sock.on("message", (data, isBinary) => {
        sock.send(data, { binary: isBinary });
      });
    });

    server.on("upgrade", (req, socket, head) => {
      lastUpgrade = { path: req.url, headers: req.headers };
      if (rejectNextUpgrade) {
        const { status, headers, body } = rejectNextUpgrade;
        const lines: string[] = [`HTTP/1.1 ${status} Livepeer-Error`];
        for (const [k, v] of Object.entries(headers)) lines.push(`${k}: ${v}`);
        lines.push(`Content-Length: ${Buffer.byteLength(body, "utf8")}`);
        lines.push("Connection: close", "", body);
        socket.write(lines.join("\r\n"));
        socket.destroy();
        rejectNextUpgrade = undefined;
        return;
      }
      wss.handleUpgrade(req, socket, head, (ws) => {
        wss.emit("connection", ws, req);
      });
    });

    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    port = (server.address() as AddressInfo).port;
  });

  after(async () => {
    wss.close();
    await new Promise<void>((resolve) => server.close(() => resolve()));
  });

  it("MODE constant matches the spec", () => {
    assert.equal(MODE, "ws-realtime@v0");
  });

  it("sets the five required Livepeer-* headers on the upgrade", async () => {
    const conn = await connect(
      { url: `http://127.0.0.1:${port}` },
      {
        capability: "openai:realtime",
        offering: "default",
        paymentBlob: "stub-payment",
        requestId: "req-ws-1",
      },
    );

    assert.ok(lastUpgrade, "server saw an upgrade");
    assert.equal(lastUpgrade!.path, "/v1/cap");
    assert.equal(lastUpgrade!.headers[HEADER.CAPABILITY.toLowerCase()], "openai:realtime");
    assert.equal(lastUpgrade!.headers[HEADER.OFFERING.toLowerCase()], "default");
    assert.equal(lastUpgrade!.headers[HEADER.PAYMENT.toLowerCase()], "stub-payment");
    assert.equal(lastUpgrade!.headers[HEADER.SPEC_VERSION.toLowerCase()], SPEC_VERSION);
    assert.equal(lastUpgrade!.headers[HEADER.MODE.toLowerCase()], "ws-realtime@v0");
    assert.equal(lastUpgrade!.headers[HEADER.REQUEST_ID.toLowerCase()], "req-ws-1");

    conn.close();
    await conn.closed;
  });

  it("relays text frames and tracks bytes", async () => {
    const conn = await connect(
      { url: `http://127.0.0.1:${port}` },
      {
        capability: "openai:realtime",
        offering: "default",
        paymentBlob: "stub",
        requestId: "req-ws-echo",
      },
    );

    const echoed = new Promise<string>((resolve) => {
      conn.onMessage((data) => {
        resolve(data.toString());
      });
    });

    conn.send("hello-broker");
    const reply = await echoed;
    assert.equal(reply, "hello-broker");
    assert.equal(conn.bytesOut(), Buffer.byteLength("hello-broker", "utf8"));
    assert.equal(conn.bytesIn(), Buffer.byteLength("hello-broker", "utf8"));

    conn.close();
    const result = await conn.closed;
    assert.equal(result.workUnits, 0);
  });

  it("throws LivepeerBrokerError when the broker rejects the upgrade", async () => {
    rejectNextUpgrade = {
      status: 402,
      headers: {
        "Content-Type": "application/json",
        [HEADER.ERROR]: "payment_invalid",
        [HEADER.REQUEST_ID]: "req-ws-rej",
      },
      body: '{"error":"payment_invalid","message":"insufficient escrow"}',
    };

    await assert.rejects(
      connect(
        { url: `http://127.0.0.1:${port}` },
        {
          capability: "openai:realtime",
          offering: "default",
          paymentBlob: "bad",
        },
      ),
      (err: unknown) => {
        assert.ok(err instanceof LivepeerBrokerError);
        const e = err as LivepeerBrokerError;
        assert.equal(e.status, 402);
        assert.equal(e.code, "payment_invalid");
        assert.equal(e.message, "insufficient escrow");
        return true;
      },
    );
  });

  it("uses the debitsClient to surface a final work-units count on close", async () => {
    const stub: SessionDebitsClient = {
      async getSessionDebits() {
        return { totalWorkUnits: 4096, debitCount: 3, closed: true };
      },
    };

    const conn = await connect(
      { url: `http://127.0.0.1:${port}` },
      {
        capability: "openai:realtime",
        offering: "default",
        paymentBlob: "stub",
        requestId: "req-ws-final",
        debitsClient: stub,
        sender: new Uint8Array(20),
      },
    );

    conn.close();
    const result = await conn.closed;
    assert.equal(result.workUnits, 4096);
  });
});
