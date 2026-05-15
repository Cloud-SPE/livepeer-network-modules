import test from "node:test";
import assert from "node:assert/strict";
import { tmpdir } from "node:os";
import path from "node:path";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";

import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";

import { createOrchSelector } from "../src/orchSelector.js";
import type { Config } from "../src/config.js";

async function dirExists(p: string): Promise<boolean> {
  try {
    return (await fs.stat(p)).isDirectory();
  } catch {
    return false;
  }
}

async function locateResolverProtoRoot(): Promise<string | null> {
  let dir = path.dirname(fileURLToPath(import.meta.url));
  for (let i = 0; i < 10; i++) {
    const candidate = path.join(dir, "proto-contracts");
    if (await dirExists(candidate)) return candidate;
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  return null;
}

async function startStubResolver(socketPath: string, protoRoot: string): Promise<grpc.Server> {
  const def = await protoLoader.load(
    ["livepeer/registry/v1/types.proto", "livepeer/registry/v1/resolver.proto"],
    {
      keepCase: false,
      longs: String,
      enums: String,
      defaults: true,
      oneofs: true,
      includeDirs: [protoRoot],
    },
  );
  const proto = grpc.loadPackageDefinition(def) as unknown as {
    livepeer: { registry: { v1: { Resolver: { service: grpc.ServiceDefinition } } } };
  };

  const server = new grpc.Server();
  server.addService(proto.livepeer.registry.v1.Resolver.service, {
    listKnown: (_call: unknown, cb: grpc.sendUnaryData<unknown>) =>
      cb(null, { entries: [{ ethAddress: "0x1111111111111111111111111111111111111111" }] }),
    resolveByAddress: (_call: unknown, cb: grpc.sendUnaryData<unknown>) =>
      cb(null, {
        nodes: [
          {
            url: "http://broker.example.com",
            operatorAddress: "0x1111111111111111111111111111111111111111",
            enabled: true,
            capabilities: [
              {
                name: "daydream:scope:v1",
                workUnit: "sessions",
                offerings: [],
              },
            ],
          },
        ],
      }),
    refresh: (_call: unknown, cb: grpc.sendUnaryData<unknown>) => cb(null, {}),
    getAuditLog: (_call: unknown, cb: grpc.sendUnaryData<unknown>) => cb(null, { events: [] }),
    health: (_call: unknown, cb: grpc.sendUnaryData<unknown>) =>
      cb(null, { mode: "resolver", chainOk: true, manifestFetcherOk: true, cacheSize: 1 }),
  });
  await new Promise<void>((res, rej) => {
    server.bindAsync(`unix:${socketPath}`, grpc.ServerCredentials.createInsecure(), (err) =>
      err ? rej(err) : res(),
    );
  });
  return server;
}

test("orch selector returns no candidates when resolver has pruned unhealthy offerings", async (t) => {
  const resolverProtoRoot = await locateResolverProtoRoot();
  if (!resolverProtoRoot) {
    t.diagnostic("skipping: proto root not found");
    return;
  }
  const tmpDir = await fs.mkdtemp(path.join(tmpdir(), "daydream-orch-selector-"));
  const resolverSock = path.join(tmpDir, "resolver.sock");
  const resolverSrv = await startStubResolver(resolverSock, resolverProtoRoot);

  t.after(async () => {
    await new Promise<void>((res) => resolverSrv.tryShutdown(() => res()));
    await fs.rm(tmpDir, { recursive: true, force: true });
  });

  const cfg: Config = {
    listen: ":9100",
    payerDaemonSocket: "/tmp/payer.sock",
    resolverSocket: resolverSock,
    capabilityId: "daydream:scope:v1",
    offeringId: "default",
    interactionMode: "session-control-external-media@v0",
    resolverSnapshotTtlMs: 15_000,
    paymentProtoRoot: "/tmp/proto",
    resolverProtoRoot,
    routeFailureThreshold: 2,
    routeCooldownMs: 30_000,
  };
  const selector = createOrchSelector(cfg);
  const listed = await selector.list();
  assert.deepEqual(listed, []);
  await assert.rejects(() => selector.pickRandom(), /no orchestrators advertising capability/);
});
