---
title: Transcode pricing per frame-megapixel
date: 2026-09-05
status: reference
audience: operators, pricing work
---

# Transcode pricing per frame-megapixel

Provenance for the `price_default` values the four transcode templates
carry from 2026-09-05, when their work unit changed from `jobs` (constant
1 per request) to aggregate frame-megapixels. The operator's decision, on
the transcode team's objection that per-job pricing "changes both pricing
semantics and funded-ceiling behavior" and against this repository's own
plan 0011, which listed ABR as `frame_megapixel`-metered.

## The unit

One work unit is one million output pixel-frames:

```text
units = ceil( Σ over delivered video renditions ( frames_i × width_i × height_i ) / 1,000,000 )
```

Audio-only outputs contribute zero. The runner computes it — it has every
rendition's frame count and dimensions — and claims it in the
`Livepeer-Work-Units` trailer that ends a `stream` response (paid-job §5),
which the broker's `response-trailer` extractor reads. Rounding,
partial-failure and audio-only rules are the runner's, with the runner's
fixtures; the broker reads a number.

A 1080p30 minute is 1920 × 1080 × 1800 / 1e6 ≈ 3,732 units; a four-rung
`abr-standard` ladder (1080p, 720p, 480p, 360p) of one minute is ≈ 6,500
units.

## Anchor and price

No transcode row exists in the June snapshot. Anchor: **AWS Elemental
MediaConvert, on-demand, professional tier, HD (≤1080p) at 30 fps —
$0.015 per output minute** (read 2026-09-05). Per unit: $0.015 / 3,732 ≈
$4.02e-6. Thirty percent under, per the June method: $2.81e-6 per unit,
**$0.00281 per 1,000 units**.

```text
amount_wei = 0.00281 / 1,724.44 × 1e18 = 1,629,000,000,000   per_units 1000
```

The June ETH basis is inherited on purpose, as for every price in the
catalog; re-derive together when it moves.

## Sanity against the operator's old per-job rate

The rig charged 4.8e12 wei per job. A one-minute `abr-standard` ladder is
≈ 6,500 units → 10.6e12 wei at this rate — about 2.2× the old flat price,
which is right for a job that does four encodes; a ten-second single-rung
720p VOD is ≈ 276 units → 0.45e12, a tenth of the old flat price, which is
the point of metering by work.

## Applied to the templates

| Template | `amount_wei` per 1000 units | Note |
|---|---:|---|
| `video-transcode-vod` | `1629000000000` | as derived |
| `video-transcode-abr` | `1629000000000` | same rate; a ladder costs more because it produces more |
| `video-transcode-vod-av1-cpu` | `3258000000000` | 2×: a software AV1 encode is many times the wall-clock per pixel-frame, and its output is worth more per byte delivered |
| `video-transcode-live` | unchanged | output seconds; not a VOD job |

## Sources

- AWS Elemental MediaConvert pricing page, on-demand professional tier,
  read 2026-09-05.
