# layer-check

Architecture boundary enforcement for `chain-commons`.

The full implementation lands in plan 0001 §J. This README pins the layer
rules; the static-analysis tool reads them and rejects PRs that violate.

## Layer rules

```
chain/                pure data, no internal/ imports
errors/               pure data, no internal/ imports
config/               may import chain/, errors/
providers/            interfaces in package root; impls in subpackages
                      may import chain/, errors/, config/, stdlib, go-ethereum
                      types
services/             may import providers/, chain/, errors/, config/
                      MUST NOT import go-ethereum directly (go through
                      providers/rpc)
                      MUST NOT import bbolt directly (go through providers/store)
testing/              fakes for every provider; may import everything
```

## Forbidden imports (universal in chain-commons)

- `github.com/prometheus/client_golang` — anywhere. Daemons inject Recorder.
- `google.golang.org/grpc` — anywhere. chain-commons is a library, daemons
  build their own gRPC surfaces.
- `net/http` outside `providers/` — outbound HTTP must live behind a provider
  abstraction (the rpc provider, internally).

## Why this matters

When a daemon refactors onto chain-commons (plans 0004, 0005), the daemon's
`internal/service/` no longer imports go-ethereum or bbolt directly. If
chain-commons leaks those imports through `services/` or anywhere outside
`providers/`, the daemon inherits leaks too — and the value of the refactor
shrinks.

## Implementation

`golangci-lint`'s `depguard` linter encodes most of this in
`chain-commons/.golangci.yml`. Ad-hoc Go-tool-based static analysis lands
here once `depguard` proves insufficient (rare).
