import { describe, it, before, after } from "node:test";
import assert from "node:assert/strict";
import * as http from "node:http";
import type { AddressInfo } from "node:net";

import { send, MODE } from "../src/modes/http-reqresp.js";
import { HEADER, SPEC_VERSION } from "../src/headers.js";
import { LivepeerBrokerError } from "../src/errors.js";

interface RecordedCall {
  method: string | undefined;
  path: string | undefined;
  headers: http.IncomingHttpHeaders;
  body: string;
}

describe("http-reqresp middleware", () => {
  let server: http.Server;
  let port: number;
  let lastCall: RecordedCall | undefined;
  let nextResponse: { status: number; headers: Record<string, string>; body: string } = {
    status: 200,
    headers: { "Content-Type": "application/json", [HEADER.WORK_UNITS]: "42", [HEADER.REQUEST_ID]: "req-mock" },
    body: '{"ok":true}',
  };

  before(async () => {
    server = http.createServer((req, res) => {
      let body = "";
      req.on("data", (c: Buffer) => {
        body += c.toString("utf-8");
      });
      req.on("end", () => {
        lastCall = { method: req.method, path: req.url, headers: req.headers, body };
        for (const [k, v] of Object.entries(nextResponse.headers)) {
          res.setHeader(k, v);
        }
        res.writeHead(nextResponse.status);
        res.end(nextResponse.body);
      });
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    port = (server.address() as AddressInfo).port;
  });

  after(async () => {
    await new Promise<void>((resolve) => server.close(() => resolve()));
  });

  it("MODE constant matches the spec", () => {
    assert.equal(MODE, "http-reqresp@v0");
  });

  it("sets the five required Livepeer-* request headers", async () => {
    const resp = await send(
      { url: `http://127.0.0.1:${port}` },
      {
        capability: "test:cap",
        offering: "default",
        paymentBlob: "stub-payment",
        body: '{"hello":"world"}',
        contentType: "application/json",
      },
    );
    assert.equal(resp.status, 200);
    assert.equal(resp.workUnits, 42);
    assert.equal(resp.requestId, "req-mock");

    assert.ok(lastCall, "mock recorded a call");
    assert.equal(lastCall!.method, "POST");
    assert.equal(lastCall!.path, "/v1/cap");
    assert.equal(lastCall!.headers[HEADER.CAPABILITY.toLowerCase()], "test:cap");
    assert.equal(lastCall!.headers[HEADER.OFFERING.toLowerCase()], "default");
    assert.equal(lastCall!.headers[HEADER.PAYMENT.toLowerCase()], "stub-payment");
    assert.equal(lastCall!.headers[HEADER.SPEC_VERSION.toLowerCase()], SPEC_VERSION);
    assert.equal(lastCall!.headers[HEADER.MODE.toLowerCase()], "http-reqresp@v0");
    assert.equal(lastCall!.body, '{"hello":"world"}');
  });

  it("forwards Livepeer-Request-Id when provided", async () => {
    await send(
      { url: `http://127.0.0.1:${port}` },
      {
        capability: "test:cap",
        offering: "default",
        paymentBlob: "stub",
        body: "{}",
        contentType: "application/json",
        requestId: "req-from-gateway",
      },
    );
    assert.equal(lastCall!.headers[HEADER.REQUEST_ID.toLowerCase()], "req-from-gateway");
  });

  it("returns workUnits = 0 when Livepeer-Work-Units header is absent", async () => {
    nextResponse = { status: 200, headers: { "Content-Type": "application/json" }, body: "{}" };
    const resp = await send(
      { url: `http://127.0.0.1:${port}` },
      { capability: "c", offering: "o", paymentBlob: "p", body: "{}", contentType: "application/json" },
    );
    assert.equal(resp.workUnits, 0);
  });

  it("throws LivepeerBrokerError on non-2xx; surfaces error code + backoff", async () => {
    nextResponse = {
      status: 503,
      headers: {
        "Content-Type": "application/json",
        [HEADER.ERROR]: "capacity_exhausted",
        [HEADER.BACKOFF]: "30",
        [HEADER.REQUEST_ID]: "req-503",
      },
      body: '{"error":"capacity_exhausted","message":"broker has no slots"}',
    };
    await assert.rejects(
      send(
        { url: `http://127.0.0.1:${port}` },
        { capability: "c", offering: "o", paymentBlob: "p", body: "{}", contentType: "application/json" },
      ),
      (err: unknown) => {
        assert.ok(err instanceof LivepeerBrokerError, `expected LivepeerBrokerError, got ${err}`);
        const e = err as LivepeerBrokerError;
        assert.equal(e.status, 503);
        assert.equal(e.code, "capacity_exhausted");
        assert.equal(e.backoffSeconds, 30);
        assert.equal(e.requestId, "req-503");
        assert.equal(e.message, "broker has no slots");
        return true;
      },
    );
    // Restore default for subsequent tests in this file.
    nextResponse = {
      status: 200,
      headers: { "Content-Type": "application/json", [HEADER.WORK_UNITS]: "42", [HEADER.REQUEST_ID]: "req-mock" },
      body: '{"ok":true}',
    };
  });
});
