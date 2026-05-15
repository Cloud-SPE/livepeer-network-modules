import test from "node:test";
import assert from "node:assert/strict";
import { tmpdir } from "node:os";
import path from "node:path";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";

import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";

import { createServiceRegistryClient } from "../../src/providers/serviceRegistry.js";

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
            url: "http://node.example.com",
            operatorAddress: "0x1111111111111111111111111111111111111111",
            enabled: true,
            capabilities: [
              {
                name: "livepeer:vtuber-session",
                workUnit: "seconds",
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

test("service registry client returns no VTuber node when resolver has pruned unhealthy offerings", async (t) => {
  const resolverProtoRoot = await locateResolverProtoRoot();
  if (!resolverProtoRoot) {
    t.diagnostic("skipping: proto root not found");
    return;
  }
  const tmpDir = await fs.mkdtemp(path.join(tmpdir(), "vtuber-service-registry-"));
  const resolverSock = path.join(tmpDir, "resolver.sock");
  const resolverSrv = await startStubResolver(resolverSock, resolverProtoRoot);

  t.after(async () => {
    await new Promise<void>((res) => resolverSrv.tryShutdown(() => res()));
    await fs.rm(tmpDir, { recursive: true, force: true });
  });

  const client = createServiceRegistryClient({
    brokerUrl: null,
    resolverSocket: resolverSock,
    resolverProtoRoot,
    resolverSnapshotTtlMs: 15_000,
    routeFailureThreshold: 2,
    routeCooldownMs: 30_000,
  });
  const node = await client.select({
    capability: "livepeer:vtuber-session",
    offering: "default",
  });
  assert.equal(node, null);
});
