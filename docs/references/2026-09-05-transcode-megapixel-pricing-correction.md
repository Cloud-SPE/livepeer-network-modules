---
title: Correction to the transcode frame-megapixel pricing reference
date: 2026-09-05
status: reference
audience: operators, pricing work
---

# Correction to `2026-09-05-transcode-megapixel-pricing.md`

Two labels in the same-day reference were wrong, as the transcode team
pointed out; the numbers stand. References are point-in-time, so this
note supersedes those labels rather than editing them.

1. **The anchor is AWS MediaConvert Basic tier, not Professional.**
   $0.015 per output minute is the Basic-tier AVC HD (≤1080p, ≤30 fps,
   single-pass) rate — $0.0075 × 2 for HD. The Professional-tier HD rate is
   $0.024 ($0.012 × 2). The catalog keeps the $0.015 anchor and the derived
   `1629000000000` wei per 1,000 units: a pool of consumer cards is a
   single-pass encoder, and Basic is the honest comparator.

2. **The CPU AV1 multiplier is a product decision, not a derivation.**
   `video-transcode-vod-av1-cpu` at 2× is a commercial choice about what a
   software AV1 encode is worth, not something the H.264 anchor implies.
   Reprice it against real encode times and what buyers pay for AV1.
