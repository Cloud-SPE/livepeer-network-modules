# Unsigned static-pin example

This example deliberately supplies route metadata directly in YAML. It is
separate from the recommended signed coordinator discovery flow:
`manifest_url` fetches a coordinator's signed publication; `pin.url` injects an
unsigned endpoint and is never fetched as a manifest pointer.

From the component directory with the Go development toolchain:

```sh
make build
./bin/livepeer-service-registry-daemon --mode=resolver --dev   --socket=/tmp/reg.sock   --static-overlay=examples/static-overlay-only/nodes.yaml
```

In another terminal:

```sh
go run ./examples/smoke-client /tmp/reg.sock
grpcurl -plaintext -unix /tmp/reg.sock livepeer.registry.v1.Resolver.ListKnown
```

Dev without a chain seed forces overlay-only and uses an in-memory store.
Startup synthesizes pins and writes cache presence records. The example
address explicitly permits unsigned nodes. The smoke client calls Health and
ResolveByAddress; it does not prove that the illustrative broker endpoints
exist or are live/selectable. Selection may require ready broker health.

The shipped pins have capability, offering, protocol and price metadata for
illustration, but no signed settlement delegation. They are unsuitable as a
substitute for a signed manifest when a consumer needs settlement keys.

Production overlay-only is fully supported: use `manifest_url` with a real
coordinator HTTPS endpoint, omit `--dev`, and retain normal payment-daemon chain
configuration. See [signed coordinator discovery](../../docs/design-docs/static-overlay.md#signed-coordinator-discovery).
