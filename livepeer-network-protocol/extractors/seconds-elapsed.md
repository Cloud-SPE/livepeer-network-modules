---
extractor_name: seconds-elapsed
version: 0.1.0
status: accepted
spec_version: 0.1.0
last_updated: 2026-05-06
---

# Extractor: `seconds-elapsed`

Wall-clock duration. Counts seconds (or sub-second units) one `paid-job/v1`
exchange was active.

## When to use

- `stream`-transport exchanges priced by duration.
- Audio transcription priced by input audio length (when the duration is read
  from a probe step rather than the file metadata).
- Any time-based pricing model.

## Configuration in `host-config.yaml`

```yaml
work_unit:
  name: "seconds"
  extractor:
    type: "seconds-elapsed"
    granularity: 1.0          # seconds-per-unit; default 1.0
    rounding: "ceil"          # "ceil", "floor", "round"; default "ceil"
```

| Field | Required | Default | Notes |
|---|---|---|---|
| `type` | yes | — | `"seconds-elapsed"` |
| `granularity` | no | `1.0` | Seconds per work-unit (`0.1` for tenths, `60` for minute-units) |
| `rounding` | no | `"ceil"` | How to round the final tally to an integer |

## Start/end semantics

Extractors are a **`paid-job/v1` concept only** — `paid-session/v1` usage comes
from runner-reported cumulative claims, and a broker never runs an extractor on
a session. The timer therefore always spans one paid exchange, with the anchors
determined by the transport the request negotiated:

| Transport | `start` | `end` |
|---|---|---|
| `unary` | first byte of request body received | last byte of response sent |
| `stream` | first byte of request body received | `Livepeer-Work-Units` trailer emitted |
| `multipart` | first byte of request body received | last byte of response sent |

There are no configurable anchors: the terminal accounting point is fixed by
`paid-job/v1` §5 (response completion or stream termination), which is exactly
what makes the claim and the debit consistent.

## Recipe

1. Record `t_start` at the transport's start anchor.
2. Record `t_end` at the transport's end anchor.
3. Compute `elapsed = t_end - t_start` in seconds (floating-point).
4. Compute `units = elapsed / granularity`.
5. Apply `rounding` to get a non-negative integer.
6. That is `actualUnits`.

## Example

A long-running `stream` exchange priced at 1 unit per second:

```yaml
work_unit:
  name: "seconds"
  extractor:
    type: "seconds-elapsed"
    granularity: 1.0
    rounding: "ceil"
```

The exchange lasted 12 minutes 34.7 seconds → `elapsed = 754.7s` →
`units = 755` (ceil), claimed in the `Livepeer-Work-Units` trailer.

## Versioning

`0.1.0`.

## Conformance

- Start/end anchors match the negotiated transport's lifecycle (verified per
  transport).
- Rounding modes (`ceil`, `floor`, `round`) produce expected integers.
- Granularity > 1 produces fewer units (e.g., `granularity: 60` for minute-
  pricing).
- Sub-second granularity (`granularity: 0.1`) produces tenth-of-second
  pricing.
