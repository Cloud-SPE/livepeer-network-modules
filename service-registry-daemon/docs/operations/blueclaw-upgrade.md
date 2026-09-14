# Blueclaw registry upgrade and acceptance

Use the replacement registry image digest recorded in the
[release record](../../../infra/build/v2.0.0-release.md). The tag remains
v2.0.0; a cached image with that tag does not prove the fix is installed.
Pull and recreate the registry container, then check its --version output
against the recorded source revision. Preserve its database volume across upgrades.

Configure coordinator discovery with the operator's expected signing identity:

```yaml
overlay:
  - eth_address: "0x..."
    manifest_url: "https://coordinator.example.com/.well-known/livepeer-registry.json"
```

Run with `--discovery=overlay-only --static-overlay=/etc/livepeer/nodes.yaml`.
Bind-mount that file into the container. No chain RPC configuration is required
for this discovery mode. Use the [Compose instructions](running-the-daemon.md)
and [overlay contract](../design-docs/static-overlay.md). Do not put the broker
inventory URL in manifest_url or replace discovery with pin[].url.

## Acceptance sequence

1. Fetch the coordinator's signed envelope and confirm its expected identity,
   current validity window, monotonically increasing publication sequence,
   broker URLs, capability/offer tuples, price denominator and settlement keys.
2. Call ResolveByAddress for the configured identity. Confirm manifest-sourced,
   verified nodes. Call Select or SelectMany with an advertised capability and
   offering; verify broker URL, protocol, price, units_per_price and delegation.
   The broker must return fresh ready state at /registry/health for the tuple.
3. Change an advertised price or offering through the normal broker/coordinator
   and cold-signing flow. Publish a higher sequence. Force an address-specific
   Refresh or wait for cache TTL. Confirm Select reflects the new publication
   without changing YAML.
4. Test coordinator outage, invalid signature, expired publication and rollback.
   Only transport failure may use an unexpired same-source last-good entry
   within max-stale. Invalid publications must not become selected routes.
5. Submit work through Blueclaw's normal gateway/broker path and verify settlement
   records and ticket funding/redemption through the payment components. Overlay
   discovery bypasses serviceURI lookup only; these components retain their
   independent chain and funding requirements.

Steps involving Blueclaw's deployed coordinator, broker, gateway and funded ticket
flow require Blueclaw's environment. Component tests and local image smoke tests
are not evidence that those production integration steps have passed.
