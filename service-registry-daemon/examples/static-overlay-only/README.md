# static-overlay-only

A resolver run that ignores the chain entirely and serves nodes purely from an operator-curated `nodes.yaml`. Useful when:

- Bootstrapping (no orchestrators have published manifests yet).
- Testing consumer code without spinning up chain RPC + manifest hosting.
- Mirroring `openai-livepeer-bridge`'s static-config posture without the chain dependency.

## How it works

When `--mode=resolver --discovery=overlay-only` (forced by `--dev`), the daemon:

1. Loads `nodes.yaml` via `--static-overlay`.
2. Walks every enabled overlay entry once at startup and calls `ResolveByAddress` per address. With no chain entry behind the address, the resolver falls into the **`static-overlay` mode** synth path: it builds a result from the overlay's pin nodes alone, applies signature policy (`unsigned_allowed: true` is required when no manifest is present), and writes the result to the cache.
3. After seed completes, `ListKnown` and `Select` see every overlay address — consumers do not need to call `Refresh` or `ResolveByAddress` first.

## Run it

```sh
# Terminal 1 — start the daemon.
make build
./bin/livepeer-service-registry-daemon \
  --mode=resolver --dev \
  --socket=/tmp/reg.sock \
  --static-overlay=examples/static-overlay-only/nodes.yaml

# Terminal 2 — exercise the surface.
go run ./examples/smoke-client /tmp/reg.sock
```

The smoke client prints `Resolver.Health` (cache size > 0) and exercises `ResolveByAddress` against the overlay's address.

To inspect the seeded pool over gRPC:

```sh
grpcurl -plaintext -unix /tmp/reg.sock livepeer.registry.v1.Resolver.ListKnown
```

## What's in `nodes.yaml`

One overlay entry with two pin nodes — a transcoder and an AI worker — both `unsigned_allowed: true` so the resolver returns them in the absence of a signed manifest.

## When NOT to use this

Production deployments should set `--discovery=chain` against a real chain RPC. Overlay-only is for cases where chain isn't available or hasn't been wired up yet.
