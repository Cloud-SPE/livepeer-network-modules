# Blueclaw settlement-key relay verification — 2026-09-14

Point-in-time investigation of a report that SelectMany omitted the coordinator's
manifest-level settlement_keys. Work tracked in lnm-vc6; Blueclaw deployment
acceptance and diagnosis remain in lnm-2k1.

## Reproduction

Ran an isolated registry container from the already-published image:

`tztcloud/livepeer-service-registry-daemon@sha256:ab3a4e9812849e7b477d8753508d1655bb85326ed2274770a4941d84b0512c11`

Startup version: `v2.0.0-4d26d1b1972b`. Production mode, fresh persistent cache,
overlay-only, reject-unsigned=true. No discovery RPC was configured.

```yaml
overlay:
  - eth_address: "0xd00354656922168815fcd1e51cbddb9e359e3c7f"
    manifest_url: "https://coordinator.xode.app/.well-known/livepeer-registry.json"
```

Used the generated registry gRPC client over the container's unix socket to call
SelectMany(capability="openai:chat-completions", offering="qwen3.6-27b"). The daemon
fetched and verified the live coordinator publication and checked live broker
readiness. It returned one route for `https://ai2-rig-broker.xode.app`, publication
sequence 10, with BOTH published settlement keys and their not_before/expires_at
windows. Full proto JSON output is preserved in the
[response snapshot](2026-09-14-blueclaw-selectmany-response.json).

The existing implementation decodes keys in types/coordinator_envelope.go,
projects them onto resolved nodes in service/resolver/resolver.go, builds route
keys in runtime/grpc/convert.go, and serializes proto field 15 for SelectMany.
No production implementation change or image rebuild was required. Added a wire
regression exercising two keys on two routes for cold, cached and forced-refresh
selection, using a signed coordinator envelope and overlay-only discovery.

## Remaining deployment questions

This does not reproduce the reported empty-key response from Blueclaw's running
daemon. Obtain its image digest/startup version, redacted overlay configuration,
and actual SelectMany response. Check manifest_url rather than static pin routes;
static pins do not acquire signed delegation merely because a coordinator exists.
If the correct image and manifest source are confirmed, compare the daemon's raw
field-15 wire response with Blueclaw's generated proto decoding and route mapping.
No funded work or payment operation was performed in this verification.
