---
extractor_name: seconds-elapsed
version: 0.1.0
status: accepted
spec_version: 0.1.0
last_updated: 2026-05-06
---

# Extractor: `seconds-elapsed`

Wall-clock duration. Counts seconds (or sub-second units) the request, stream,
or session was active.

## When to use

- Sessions and streams priced by duration.
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
| `start` | no | mode-default | When the timer starts; see below |
| `end` | no | mode-default | When the timer stops; see below |

## Start/end semantics

The default timer points depend on the mode:

| Mode | Default `start` | Default `end` |
|---|---|---|
| `http-reqresp` | first byte of request body received | last byte of response sent |
| `http-stream` | first byte of request body received | trailer emitted |
| `http-multipart` | first byte of request body received | last byte of response sent |
| `ws-realtime` | upgrade complete (after `101 Switching Protocols`) | close frame exchanged |
| `rtmp-ingress-hls-egress` | first RTMP packet received | session-end (or RTMP disconnect) |
| `session-control-plus-media` | session-open response (202) sent | `session.end` event or auto-close |

Implementations MAY override `start` / `end` with named anchors:

- `request_received` — first request byte arrived
- `response_started` — first response byte sent
- `upgrade_complete` — WebSocket upgrade succeeded
- `session_started` — session resource allocated
- `session_ended` — session reconcile complete

## Recipe

1. Record `t_start` per the mode's default (or override).
2. Record `t_end` per the mode's default (or override).
3. Compute `elapsed = t_end - t_start` in seconds (floating-point).
4. Compute `units = elapsed / granularity`.
5. Apply `rounding` to get a non-negative integer.
6. That is `actualUnits`.

For sessions/streams using cadence-based interim debits, the per-tick value is
`(now - last_tick) / granularity`, rounded per the spec.

## Example

A vtuber session priced at 1 unit per second:

```yaml
work_unit:
  name: "seconds"
  extractor:
    type: "seconds-elapsed"
    granularity: 1.0
    rounding: "ceil"
```

Session lasted 12 minutes 34.7 seconds → `elapsed = 754.7s` → `units = 755`
(ceil).

## Versioning

`0.1.0`.

## Conformance

- Default start/end anchors match the mode's lifecycle (verified per mode).
- Override anchors honored when specified.
- Rounding modes (`ceil`, `floor`, `round`) produce expected integers.
- Granularity > 1 produces fewer units (e.g., `granularity: 60` for minute-
  pricing).
- Sub-second granularity (`granularity: 0.1`) produces tenth-of-second
  pricing.
- For interim-debit modes: per-tick units sum exactly to the total over the
  session lifetime.
