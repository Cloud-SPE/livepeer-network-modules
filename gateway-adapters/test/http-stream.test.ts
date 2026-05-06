import { describe, it, before, after } from "node:test";
import assert from "node:assert/strict";
import * as http from "node:http";
import type { AddressInfo } from "node:net";

import { send, MODE } from "../src/modes/http-stream.js";
import { HEADER } from "../src/headers.js";

describe("http-stream middleware", () => {
  let server: http.Server;
  let port: number;

  before(async () => {
    // Mock broker that responds with a streaming body and emits
    // Livepeer-Work-Units as an HTTP/1.1 trailer.
    server = http.createServer((req, res) => {
      let body = "";
      req.on("data", (c: Buffer) => {
        body += c.toString("utf-8");
      });
      req.on("end", () => {
        res.setHeader("Content-Type", "text/event-stream");
        res.setHeader("Trailer", HEADER.WORK_UNITS);
        res.setHeader(HEADER.REQUEST_ID, "req-stream-mock");
        res.writeHead(200);
        // Two chunks then close, with the trailer set after.
        res.write('data: {"chunk":1}\n\n');
        res.write('data: {"chunk":2,"usage":{"total_tokens":482}}\n\n');
        res.addTrailers({ [HEADER.WORK_UNITS]: "482" });
        res.end();
        // Reference body so tslint doesn't complain about unused.
        void body;
      });
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    port = (server.address() as AddressInfo).port;
  });

  after(async () => {
    await new Promise<void>((resolve) => server.close(() => resolve()));
  });

  it("MODE constant matches the spec", () => {
    assert.equal(MODE, "http-stream@v0");
  });

  it("reads Livepeer-Work-Units from the response trailer", async () => {
    const resp = await send(
      { url: `http://127.0.0.1:${port}` },
      {
        capability: "test:s",
        offering: "default",
        paymentBlob: "stub",
        body: '{"prompt":"streamy","stream":true}',
        contentType: "application/json",
      },
    );
    assert.equal(resp.status, 200);
    assert.equal(resp.workUnits, 482);
    assert.equal(resp.requestId, "req-stream-mock");
    assert.ok(resp.body.toString("utf-8").includes("data: {"));
  });
});
