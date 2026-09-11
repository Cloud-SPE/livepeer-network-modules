---
title: CPU as a placeable compute unit
status: implementing
date: 2026-09-02
beads: lnm-iqn
supersedes: none
---

# 0047 — CPU as a placeable compute unit

## 1. Purpose

The placement model was one service per GPU: a `HardwareUnit` was a card
with a UUID, placement pinned a service to it, GPU-UUID uniqueness was the
cross-member rule, and the vendor image map selected by GPU vendor. A
CPU-only host attached with `hardware: []` — valid, but with nothing to
place on — so a member could never hold a CPU workload, certify through
the ladder, or earn. For VOD, SVT-AV1 on CPU is the *better* AV1 encoder,
which makes that a premium path the pool could not sell (decision 7 of the
2026-09-02 walkthrough, plan 0045 §11).

This plan makes the placeable thing a compute unit, of which the GPU was
the first kind.

## 2. Decisions

1. **A socket is a hardware unit with `kind: cpu`** (runner-attach §3.1,
   contract 1.2). It keeps the GPU-named fields — `gpu_uuid` is its stable
   id `cpu-<host_id>-<socket>`, `gpu_model` the CPU model string,
   `vram_bytes` 0 — because renaming them is a major and the fields mean
   the same things. It adds `cores`, `threads`, `isa`.
2. **The agent reports every socket, always**, from `/proc/cpuinfo`.
   Placement admits a socket only to a template that lists `cpu_classes`,
   so a catalog with no CPU workload puts a rejection on the exception
   queue for the CPU templates only; the host's cards are unaffected.
3. **CPU classes are core tiers**: `cpu-8`, `cpu-16`, `cpu-32`, `cpu-64`
   — the largest tier a socket meets. Under 8 cores is not a tier the pool
   sells for and is named on the exception queue with its core count.
4. **The kinds never compete.** A socket is admitted only by a template
   listing `cpu_classes` (never the default winner of an unconstrained
   template — the §5 failure mode for cards, worse here), and a template
   listing only `cpu_classes` never takes a card (`kind_not_allowed`).
5. **`cpu` is an image-map key**, beside the GPU vendors: same question —
   which build runs here — one more answer. A socket's service renders
   the `cpu` image and no device block.
6. **One service per socket**, one stance (1), GPU-UUID uniqueness
   generalises unchanged because the id carries the host.

## 3. What this does not do

- Ship a CPU transcode image. `video-transcode-vod-av1-cpu` is in the
  catalog with `runner_compose` omitted; the transcode packet asks whether
  `codecs-builder` produces an SVT-AV1 build, and the tag that ships it
  belongs under the `cpu` key. Until then a socket places there and
  renders no service — the catalog's existing shape for an unresolved
  image.
- Let a template place "on the host" unpinned. Every service is pinned to
  one unit; that invariant is what makes share caps meaningful.
- NUMA, pinning cores to a container, or two services on one socket.

## 4. Implementation record

| § | Commit | What |
|---|---|---|
| all | `477c96d` | Contract fields; agent cpuinfo inventory; broker validation; controller types, relay, `cpu` image key, core tiers, kind rule in placement and Validate, no device block; the AV1-on-CPU VOD template. |
