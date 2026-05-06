# Plan 0007 — Additional extractors

**Status:** completed (2026-05-06)
**Opened:** 2026-05-06
**Closed:** 2026-05-06
**Owner:** initial author + assistant

## Goal

Implement the five extractors specified in
`livepeer-network-protocol/extractors/`:

- `openai-usage` — read `usage.{prompt|completion|total}_tokens` from
  OpenAI-shaped response JSON.
- `request-formula` — safe arithmetic over request fields.
- `bytes-counted` — tally bytes through the broker (request / response /
  both).
- `seconds-elapsed` — wall-clock duration of the request.
- `ffmpeg-progress` — parse FFmpeg `-progress` output (frame /
  frame-megapixel / out-time-seconds units).

## Scope

- `internal/extractors/<name>/` package per extractor; each implements
  `extractors.Extractor` + a Factory and registers in
  `defaultExtractors()`.
- `extractors.Response.Duration` field (new) populated by the HTTP mode
  drivers so `seconds-elapsed` has a real value to read.
- `request-formula` parser uses Go's `go/parser` + AST walk to enforce
  the safe-grammar restriction at config-load time (per the spec's
  security-critical section).
- One fixture per extractor under `fixtures/<existing-mode>/` exercising
  the new extractor against the live broker.
- Five new test capabilities in `conformance/test-broker-config.yaml`.

## Done condition

`make -C livepeer-network-protocol/conformance test-compose` exits 0
with 11 fixtures passing (6 existing + 5 new).
