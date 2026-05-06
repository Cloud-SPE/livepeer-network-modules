import { describe, it, before, after } from "node:test";
import assert from "node:assert/strict";
import * as http from "node:http";
import type { AddressInfo } from "node:net";

import { send, MODE } from "../src/modes/http-multipart.js";
import { HEADER } from "../src/headers.js";

describe("http-multipart middleware", () => {
  let server: http.Server;
  let port: number;
  let lastContentType: string | undefined;
  let lastBody: Buffer = Buffer.alloc(0);

  before(async () => {
    server = http.createServer((req, res) => {
      lastContentType = req.headers["content-type"];
      const chunks: Buffer[] = [];
      req.on("data", (c: Buffer) => {
        chunks.push(c);
      });
      req.on("end", () => {
        lastBody = Buffer.concat(chunks);
        res.setHeader("Content-Type", "application/json");
        res.setHeader(HEADER.WORK_UNITS, "21");
        res.writeHead(200);
        res.end('{"text":"transcribed"}');
      });
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    port = (server.address() as AddressInfo).port;
  });

  after(async () => {
    await new Promise<void>((resolve) => server.close(() => resolve()));
  });

  it("MODE constant matches the spec", () => {
    assert.equal(MODE, "http-multipart@v0");
  });

  it("sends a FormData body and lets fetch set the boundary", async () => {
    const fd = new FormData();
    fd.append("file", new Blob(["hello world"], { type: "text/plain" }), "hello.txt");
    fd.append("model", "test-model");

    const resp = await send(
      { url: `http://127.0.0.1:${port}` },
      { capability: "test:m", offering: "default", paymentBlob: "stub", body: fd },
    );

    assert.equal(resp.status, 200);
    assert.equal(resp.workUnits, 21);
    assert.ok(lastContentType?.startsWith("multipart/form-data; boundary="),
      `expected multipart/form-data Content-Type with boundary, got ${lastContentType}`);
    assert.ok(lastBody.toString("utf-8").includes("hello world"),
      "backend should have received the file content");
    assert.ok(lastBody.toString("utf-8").includes("test-model"),
      "backend should have received the form field");
  });

  it("requires contentType when body is not FormData", async () => {
    await assert.rejects(
      send(
        { url: `http://127.0.0.1:${port}` },
        { capability: "c", offering: "o", paymentBlob: "p", body: Buffer.from("raw") },
      ),
      (err: unknown) => {
        assert.ok(err instanceof Error);
        assert.match((err as Error).message, /contentType is required/);
        return true;
      },
    );
  });
});
