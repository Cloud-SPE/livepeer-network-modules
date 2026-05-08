# minimal-e2e

End-to-end demo of the publisher → chain → resolver → consumer pipeline, all in-process. No external services required.

## What it does

1. Creates an in-memory chain (no real RPC).
2. Spins up a publisher, builds + signs a manifest with two AI nodes and one transcoding node, "publishes" it (writes the on-chain pointer in-memory + caches the manifest body).
3. Spins up a resolver pointed at the same in-memory chain + a fetcher that returns the cached manifest.
4. The consumer asks the resolver to find the orchestrator's nodes and to `Select` one gateway-facing route by capability + offering.

## Run

```sh
go run ./examples/minimal-e2e/...
```

You should see logs showing:
- A signed manifest with three nodes (two AI, one transcoding).
- A resolver call that returns all three with `signature_status: signed-verified`.
- A `Select(capability="livepeer:transcoder/h264", offering="h264-main")` call returning one explicit selected route.
- A `Select(capability="openai:/v1/chat/completions", offering="gpt-oss-20b")` call returning one explicit selected route.

## What this demonstrates

- Capability strings are opaque — the registry treats AI and transcoding identically.
- Signature verification works end-to-end against a freshly-generated key.
- The resolver supports any capability the operator advertises; no daemon code change to add a new workload.
