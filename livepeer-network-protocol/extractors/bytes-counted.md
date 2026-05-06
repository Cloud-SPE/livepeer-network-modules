---
extractor_name: bytes-counted
version: 0.1.0
status: draft (proposed)
spec_version: 0.1.0
last_updated: 2026-05-06
---

# Extractor: `bytes-counted`

Tally bytes flowing through the broker for the request, response, or both.

## When to use

- Streaming workloads where bandwidth is the cost driver.
- Capabilities priced per-byte (storage uploads, large file processing).
- A cheap fallback for any capability where the more-specific extractors
  (`openai-usage`, `request-formula`) don't apply.

## Configuration in `host-config.yaml`

```yaml
work_unit:
  name: "kilobytes"
  extractor:
    type: "bytes-counted"
    direction: "response"   # or "request" or "both"
    granularity: 1024       # bytes-per-unit; default 1
```

| Field | Required | Default | Notes |
|---|---|---|---|
| `type` | yes | — | `"bytes-counted"` |
| `direction` | no | `"response"` | `"request"`, `"response"`, or `"both"` |
| `granularity` | no | `1` | Bytes per work-unit. `1024` for KB, `1048576` for MB |
| `headers` | no | `false` | Include HTTP header bytes in the count |

## Recipe

1. As bytes flow through the broker (in the chosen direction), accumulate a
   running counter.
2. At end-of-request (or end-of-stream / end-of-session), divide by
   `granularity` and floor to integer.
3. That is `actualUnits`.

For streaming and session modes, the counter accumulates over the full
session; reported via the cadence pattern (interim debits at `cadence_seconds`
emit incremental units).

## Direction semantics

| Value | Counts |
|---|---|
| `"request"` | Bytes the gateway sent to the broker (request body, plus headers if `headers: true`). |
| `"response"` | Bytes the broker sent to the gateway (response body, plus trailers if applicable). |
| `"both"` | Sum of request and response bytes. |

## What's counted vs. not

- **Counted by default**: HTTP body bytes, WebSocket frame payload bytes,
  RTMP/HLS payload bytes.
- **Not counted by default**: HTTP headers, WebSocket control frames
  (ping/pong), RTMP control packets, TLS record overhead.
- Set `headers: true` to include HTTP headers (request line + headers block,
  per RFC 7230).

## Compression

Counts the **on-wire** bytes (post-compression). If the gateway sends a
gzipped body, the post-decompression size is irrelevant; the wire bytes are
what's counted. This matches what's actually billable bandwidth.

## Example

A capability priced at 1 unit per kilobyte of response:

```yaml
work_unit:
  name: "kilobytes"
  extractor:
    type: "bytes-counted"
    direction: "response"
    granularity: 1024
```

If the response body is 4096 bytes: `actualUnits = 4`.

## Versioning

`0.1.0`.

## Conformance

- Counter accumulates correctly across all body bytes.
- HTTP headers excluded by default; included when `headers: true`.
- Compression: on-wire bytes are counted, not decompressed.
- Streaming: per-tick interim debit values match the bytes seen in that tick.
- WebSocket control frames (ping/pong/close) are not counted.
- `direction: "both"` correctly sums both sides.
