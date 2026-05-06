# fixtures/

Declarative YAML test cases for the conformance runner. One folder per
interaction mode; one file per scenario inside.

**Status:** scaffold only — folders exist; fixtures are TODO as the runner's
mode drivers come online.

## Folder structure

```
fixtures/
├── http-reqresp/
├── http-stream/
├── http-multipart/
├── ws-realtime/
├── rtmp-ingress-hls-egress/
└── session-control-plus-media/
```

## Fixture file format (planned)

Each scenario is a single YAML file. The schema is TBD as the runner's
fixture loader is implemented (see
[`../runner/internal/fixtures/`](../runner/) when it lands). Anticipated
shape:

```yaml
name: "happy-path-200"
mode: "http-reqresp"
description: "vanilla success: payment validates, broker forwards, response returns with Livepeer-Work-Units"

# What the runner sends
request:
  method: POST
  path: /v1/cap
  headers:
    Livepeer-Capability: "openai:chat-completions:test"
    Livepeer-Offering: "test-offering"
    Livepeer-Payment: "<base64-payment>"
    Livepeer-Spec-Version: "0.1"
    Livepeer-Mode: "http-reqresp@v0"
    Content-Type: "application/json"
  body: { "prompt": "hello" }

# What the mock-backend should expect (side-effect assertion)
backend_expect:
  method: POST
  headers_present:
    - "Authorization"      # backend auth was injected
  headers_absent:
    - "Livepeer-Capability"  # Livepeer-* stripped before forwarding
  body: { "prompt": "hello" }

# What the runner expects to receive back
response_expect:
  status: 200
  headers_present:
    - "Livepeer-Work-Units"
  livepeer_work_units: 482  # exact match
```

Fixture files MUST be self-contained — no cross-file references — so
implementers can run individual scenarios for debugging.

## Naming convention

`<scenario-shortname>.yaml`, lowercase with dashes. Examples:

- `happy-path.yaml`
- `missing-capability-header.yaml`
- `payment-envelope-mismatch.yaml`
- `503-backoff-roundtrip.yaml`
- `gateway-disconnect-mid-stream.yaml` (http-stream)
- `expires-at-no-push.yaml` (rtmp-ingress-hls-egress)

## Conformance test matrix (TBD)

Per-mode test matrix lives in each mode spec's "Conformance" section
(`../modes/<mode>.md`). The fixtures here are the executable form of those
tests.
