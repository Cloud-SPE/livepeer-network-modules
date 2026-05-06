---
extractor_name: ffmpeg-progress
version: 0.1.0
status: draft (proposed)
spec_version: 0.1.0
last_updated: 2026-05-06
---

# Extractor: `ffmpeg-progress`

Parse FFmpeg's `-progress` output to compute frame-based units. Designed for
the video transcode workloads (VOD, ABR, RTMP-live).

## When to use

- Capabilities whose backend is an FFmpeg subprocess (the broker shells out
  rather than HTTP-forwards).
- Pricing by frame count, frame-megapixels, or processed-time.

## Configuration in `host-config.yaml`

```yaml
work_unit:
  name: "video-frame-megapixel"
  extractor:
    type: "ffmpeg-progress"
    unit: "frame_megapixel"     # or "frame", "out_time_seconds"
    width: 1920                 # required for frame_megapixel
    height: 1080                # required for frame_megapixel
```

| Field | Required | Default | Notes |
|---|---|---|---|
| `type` | yes | — | `"ffmpeg-progress"` |
| `unit` | yes | — | `"frame"`, `"frame_megapixel"`, `"out_time_seconds"` |
| `width` | when `unit: frame_megapixel` | — | Output frame width in pixels |
| `height` | when `unit: frame_megapixel` | — | Output frame height in pixels |

## How FFmpeg progress works

When invoked with `-progress pipe:1` (or to a file), FFmpeg emits key-value
pairs every ~500ms during encoding:

```
frame=120
fps=29.97
bitrate=1500.0kbits/s
total_size=234567
out_time_us=4000000
out_time=00:00:04.000000
speed=1.00x
progress=continue
```

(or `progress=end` on the final block.)

The broker reads this stream and accumulates the relevant counter.

## Recipe

| `unit` | Computation |
|---|---|
| `"frame"` | Final value of `frame=N` after `progress=end`. |
| `"frame_megapixel"` | `frame × width × height / 1_000_000`, floored to integer. |
| `"out_time_seconds"` | Final `out_time_us` divided by 1,000,000, rounded per `rounding` param (default `ceil`). |

For interim debits (RTMP-live mode):

- Broker accumulates frames-since-last-tick from successive `frame=N` reports.
- Per-tick units = delta of the chosen unit since the last tick.

## ABR caveat

For ABR transcode (multiple output renditions from one input), the broker MAY:

- Run multiple FFmpeg instances (one per rendition) and sum their progress, **or**
- Use a single FFmpeg instance with multiple outputs and configure FFmpeg's
  per-output progress reporting (`-stats_period`, separate progress files).

In either case, the extractor sums across renditions to get total
`frame_megapixel`. This is implementation detail of the broker, not the
extractor itself.

## Example

A 1080p VOD transcode of a 10-second clip at 30fps:

- Total frames: `300`.
- With `unit: frame_megapixel`, `width: 1920`, `height: 1080`:
  `300 × 1920 × 1080 / 1_000_000 = 622.08` → `actualUnits = 622`.

## Versioning

`0.1.0`.

## Conformance

- `unit: "frame"` correctly counts frames after `progress=end`.
- `unit: "frame_megapixel"` correctly computes for the declared output
  dimensions.
- `unit: "out_time_seconds"` correctly converts microseconds and rounds.
- Interim debits emit per-tick deltas (not cumulative), summing exactly to
  the total at session-end.
- `progress=end` line triggers final reconciliation.
- Crashes / abnormal terminations: extractor reports the last cleanly-parsed
  value (no overcounting).
