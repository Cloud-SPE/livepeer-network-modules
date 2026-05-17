---
mode_name: http-multipart
version: 0.1.0
status: accepted
spec_version: 0.1.0
last_updated: 2026-05-06
---

# Mode: `http-multipart`

One multipart-upload HTTP request → one HTTP response. The upload variant of
[`http-reqresp`](./http-reqresp.md) — same path, same five required headers, same
single-debit-with-reconcile payment lifecycle. Only the request body shape differs.

This is a delta document. Everything not stated here inherits from
[`http-reqresp@v0`](./http-reqresp.md).

## When to use this mode

- OpenAI audio transcriptions / translations (`/v1/audio/transcriptions`,
  `/v1/audio/translations`) — file upload + JSON metadata.
- OpenAI image edits (`/v1/images/edits`) — image upload + JSON params.
- Any capability whose request carries one or more files alongside JSON parameters.

## When NOT to use this mode

- JSON-only request → use [`http-reqresp`](./http-reqresp.md).
- Streaming response → either use this mode if the request is multipart and the
  response streams (combine via `Content-Type: text/event-stream` on response;
  trailer rules from `http-stream` apply), or use `http-stream` if the request is
  JSON.
- Bidirectional → `ws-realtime`.

## Delta from `http-reqresp`

### Request — `multipart/form-data` body

```
POST /v1/cap HTTP/1.1
Host: broker-a.orch.example.com
Livepeer-Capability: openai:audio-transcriptions
Livepeer-Offering: whisper-h100
Livepeer-Payment: <base64-encoded payment envelope>
Livepeer-Spec-Version: 0.1
Livepeer-Mode: http-multipart@v0
Livepeer-Request-Id: 550e8400-e29b-41d4-a716-446655440000
Content-Type: multipart/form-data; boundary=----WebKitFormBoundary7MA4YWxkTrZu0gW

------WebKitFormBoundary7MA4YWxkTrZu0gW
Content-Disposition: form-data; name="file"; filename="audio.mp3"
Content-Type: audio/mpeg

<binary audio bytes>
------WebKitFormBoundary7MA4YWxkTrZu0gW
Content-Disposition: form-data; name="model"

whisper-large-v3
------WebKitFormBoundary7MA4YWxkTrZu0gW
Content-Disposition: form-data; name="response_format"

json
------WebKitFormBoundary7MA4YWxkTrZu0gW--
```

- Same path (`POST /v1/cap`), same five required Livepeer-* headers.
- `Content-Type: multipart/form-data` with a boundary (per RFC 7578).
- Multipart parts are opaque to the protocol; the capability defines required
  field names and file types.

### Response

Identical to `http-reqresp` (one HTTP response with `Livepeer-Work-Units` in normal
headers) — *unless* the capability's response is itself a stream, in which case
this mode borrows `http-stream`'s trailer-based reporting. The mode does not
mandate either; the offering's declared `Content-Type` on the response side
dictates which.

## Payment lifecycle

Same as `http-reqresp`: estimate → debit-up-front → forward to backend → compute
`actualUnits` from the response (or from the upload size) via the offering's
extractor → reconcile → close. Per-request, no long-lived state.

The estimate (`expected_max_units`) is typically derived from the request — e.g.:

- For Whisper-shaped capabilities: estimate = `audio_duration_seconds` (read from
  the multipart file's headers/metadata pre-upload, or via a quick probe).
- For image-edits-shaped capabilities: estimate from the input image dimensions.

The `request-formula` and `seconds-elapsed` extractors (see `extractors/`) are the
typical recipes.

## Forwarding behavior

The broker:

- Streams the inbound multipart body to the backend without buffering the whole
  upload in memory. Implementations MUST support streaming forward to handle
  large files (audio up to 25 MB+ for Whisper, images up to 50 MB+).
- Strips all `Livepeer-*` headers (per the headers spec) before forwarding.
- Injects backend-specific auth from `host-config.yaml`.
- Forwards `Content-Type: multipart/form-data; boundary=...` unchanged.
- Forwards each multipart part unchanged (file bytes + form fields).
- Returns the backend's response unchanged with `Livepeer-Work-Units` set.

The gateway:

- Builds the multipart body per the capability's expectations.
- Estimates `expected_max_units` from the file metadata or other request fields.
- Same envelope/header rules as `http-reqresp`.

## Body size

- Mode does NOT impose a hard cap on upload size.
- **Recommended defaults** (not normative):
  - 100 MiB request body (audio + metadata).
  - 50 MiB response body.
- Implementations document their actual cap via `extra.max_request_bytes` and
  `extra.max_response_bytes` on the offering.
- 413 Payload Too Large is the appropriate response when an implementation's
  cap is exceeded; `Livepeer-Error: backend_unavailable` (or a
  capability-defined code) accompanies it.

## Timeouts

- Upload-side timeout: gateway and broker SHOULD allow time proportional to the
  upload size (Whisper transcription of a 1-hour audio file may take 5+ minutes).
- Capability MAY advertise an upload-side timeout via `extra.timeout_seconds`.
- Broker SHOULD detect stalled uploads (no bytes for N seconds; recommended 60s)
  and abort.

## Idempotency

Same as `http-reqresp`: not promised by the mode.

## Observability

In addition to `http-reqresp` metrics:

- `livepeer_mode_upload_bytes_total{mode="http-multipart",capability,offering}` —
  counter.
- `livepeer_mode_upload_duration_seconds{mode="http-multipart",capability,offering}` —
  histogram.

## Versioning

Per-mode SemVer. Currently `0.1.0`.

## Conformance

Tests, at minimum:

- Happy path: multipart upload completes; backend response returned with
  `Livepeer-Work-Units` set.
- Header validation: same matrix as `http-reqresp`.
- Forwarding: broker strips `Livepeer-*` and injects declared backend auth; the
  outbound request is multipart with the same boundary, parts, and bodies.
- Streaming forward: broker does not buffer the entire upload before sending to
  the backend (verified by injecting a slow-reader backend and asserting the
  broker forwards bytes as they arrive).
- Body size enforcement: requests exceeding the implementation's declared cap
  receive 413 with the appropriate error code.

Fixtures: `conformance/fixtures/http-multipart/*.yaml`.

## Changelog

| Mode version | Date | Change |
|---|---|---|
| 0.1.0 | 2026-05-06 | Initial draft. |
