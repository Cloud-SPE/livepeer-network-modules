import { describe, it, before, after } from "node:test";
import assert from "node:assert/strict";
import * as http from "node:http";
import type { AddressInfo } from "node:net";

import { WebSocketServer, type WebSocket as ServerWebSocket } from "ws";

import {
  openSession,
  connectControl,
  MODE,
} from "../src/modes/session-control-plus-media.js";
import { HEADER, SPEC_VERSION } from "../src/headers.js";
import { LivepeerBrokerError } from "../src/errors.js";
import type { SessionDebitsClient } from "../src/payer-daemon.js";

interface RecordedHttpCall {
  method: string | undefined;
  path: string | undefined;
  headers: http.IncomingHttpHeaders;
  body: string;
}

describe("session-control-plus-media middleware", () => {
  let server: http.Server;
  let wss: WebSocketServer;
  let port: number;
  let lastHttpCall: RecordedHttpCall | undefined;
  const sessionId = "sess_test_xyz";

  before(async () => {
    server = http.createServer((req, res) => {
      let body = "";
      req.on("data", (c: Buffer) => {
        body += c.toString("utf-8");
      });
      req.on("end", () => {
        lastHttpCall = { method: req.method, path: req.url, headers: req.headers, body };
        if (req.url === "/v1/cap" && req.method === "POST") {
          res.setHeader("Content-Type", "application/json");
          res.setHeader(HEADER.REQUEST_ID, "req-session-1");
          res.statusCode = 202;
          res.end(
            JSON.stringify({
              session_id: sessionId,
              control_url: `ws://127.0.0.1:${port}/v1/cap/${sessionId}/control`,
              media: { schema: "vtuber:trickle@v0", publish_url: "https://trickle.example.com/x" },
              expires_at: "2026-05-06T13:34:56Z",
            }),
          );
          return;
        }
        res.statusCode = 404;
        res.end();
      });
    });

    wss = new WebSocketServer({ noServer: true });
    wss.on("connection", (sock: ServerWebSocket) => {
      sock.send(JSON.stringify({ type: "session.started", session_id: sessionId }));
      sock.on("message", (data) => {
        try {
          const msg = JSON.parse(data.toString()) as { type?: string };
          if (msg.type === "set_persona") {
            sock.send(JSON.stringify({ type: "session.usage.tick", units: 10 }));
          } else if (msg.type === "session.end") {
            sock.send(JSON.stringify({ type: "session.ended", graceful: true }));
            sock.close(1000, "bye");
          }
        } catch {
          // ignore
        }
      });
    });

    server.on("upgrade", (req, socket, head) => {
      if (req.url?.endsWith("/control")) {
        wss.handleUpgrade(req, socket, head, (ws) => {
          wss.emit("connection", ws, req);
        });
        return;
      }
      socket.destroy();
    });

    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    port = (server.address() as AddressInfo).port;
  });

  after(async () => {
    wss.close();
    await new Promise<void>((resolve) => server.close(() => resolve()));
  });

  it("MODE constant matches the spec", () => {
    assert.equal(MODE, "session-control-plus-media@v0");
  });

  it("openSession posts /v1/cap with the five required Livepeer-* headers", async () => {
    const desc = await openSession(
      { url: `http://127.0.0.1:${port}` },
      {
        capability: "livepeer:vtuber-session",
        offering: "default",
        paymentBlob: "stub",
        body: { persona: "frieren" },
      },
    );

    assert.equal(desc.sessionId, sessionId);
    assert.equal(desc.requestId, "req-session-1");
    assert.equal(desc.expiresAt, "2026-05-06T13:34:56Z");
    assert.ok(desc.media);
    assert.equal((desc.media as { publish_url: string }).publish_url, "https://trickle.example.com/x");

    assert.ok(lastHttpCall);
    assert.equal(lastHttpCall!.method, "POST");
    assert.equal(lastHttpCall!.path, "/v1/cap");
    assert.equal(lastHttpCall!.headers[HEADER.CAPABILITY.toLowerCase()], "livepeer:vtuber-session");
    assert.equal(lastHttpCall!.headers[HEADER.OFFERING.toLowerCase()], "default");
    assert.equal(lastHttpCall!.headers[HEADER.PAYMENT.toLowerCase()], "stub");
    assert.equal(lastHttpCall!.headers[HEADER.SPEC_VERSION.toLowerCase()], SPEC_VERSION);
    assert.equal(lastHttpCall!.headers[HEADER.MODE.toLowerCase()], "session-control-plus-media@v0");
    assert.equal(JSON.parse(lastHttpCall!.body).persona, "frieren");
  });

  it("connectControl decodes session.started and forwards capability messages", async () => {
    const desc = await openSession(
      { url: `http://127.0.0.1:${port}` },
      {
        capability: "livepeer:vtuber-session",
        offering: "default",
        paymentBlob: "stub",
        body: {},
      },
    );

    const conn = await connectControl(desc.controlUrl, { requestId: desc.requestId });

    const started = await new Promise<Record<string, unknown>>((resolve) => {
      conn.on("session.started", (payload) => resolve(payload));
    });
    assert.equal(started.session_id, sessionId);

    const tick = new Promise<Record<string, unknown>>((resolve) => {
      conn.on("session.usage.tick", (payload) => resolve(payload));
    });
    conn.send({ type: "set_persona", persona: "frieren" });
    const tickPayload = await tick;
    assert.equal(tickPayload.units, 10);

    conn.send({ type: "session.end" });
    const result = await conn.closed;
    assert.equal(result.code, 1000);
    assert.equal(result.workUnits, 0);
  });

  it("openSession surfaces broker 4xx as LivepeerBrokerError", async () => {
    // Deliberately use the path the test server's request handler has
    // a 404 fallback for; openSession treats any >=400 as an error.
    const otherServer = http.createServer((_req, res) => {
      res.setHeader("Content-Type", "application/json");
      res.setHeader(HEADER.ERROR, "payment_invalid");
      res.statusCode = 402;
      res.end('{"error":"payment_invalid","message":"insufficient escrow"}');
    });
    await new Promise<void>((resolve) => otherServer.listen(0, "127.0.0.1", resolve));
    const otherPort = (otherServer.address() as AddressInfo).port;

    try {
      await assert.rejects(
        openSession(
          { url: `http://127.0.0.1:${otherPort}` },
          {
            capability: "x",
            offering: "y",
            paymentBlob: "z",
            body: {},
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
    } finally {
      await new Promise<void>((resolve) => otherServer.close(() => resolve()));
    }
  });

  it("connectControl uses debitsClient on close", async () => {
    const desc = await openSession(
      { url: `http://127.0.0.1:${port}` },
      {
        capability: "livepeer:vtuber-session",
        offering: "default",
        paymentBlob: "stub",
        body: {},
        requestId: "req-debits",
      },
    );

    const stub: SessionDebitsClient = {
      async getSessionDebits() {
        return { totalWorkUnits: 1234, debitCount: 7, closed: true };
      },
    };

    const conn = await connectControl(desc.controlUrl, {
      requestId: desc.requestId,
      sender: new Uint8Array(20),
      debitsClient: stub,
    });

    conn.send({ type: "session.end" });
    const result = await conn.closed;
    assert.equal(result.workUnits, 1234);
  });
});
