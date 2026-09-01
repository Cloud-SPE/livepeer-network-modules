# Vision pricing and the FLUX anchor — 2026-09-01

Point-in-time reference, superseding two things in
[`2026-06-15-openai-compatible-market-pricing.md`](./2026-06-15-openai-compatible-market-pricing.md)
without editing it: that document had no image-analysis row, and its FLUX row
records a price the catalog does not use. The June document stays what it was
on the day; this one records what changed and why.

## Basis

Same as June, so the two are comparable line for line:

```text
ETH/USD = 1,724.44
$1 = 1e18 / 1,724.44 wei = 579,898,982,000,000 wei (5.79899e14)
target = 30% below the selected public market anchor
```

Every figure below is derived from a number already in the June table, so it
inherits that basis with no rounding drift and can be checked against it.

## Image analysis — `vision:image-analysis` / Florence-2-large

The catalog shipped this at `25000000 wei/image`, which the operator's own
config marked as a placeholder to re-derive. At the basis above that is about
$0.000000043 per image — 4 cents per million.

**Anchor:** Google Cloud Vision, ~$0.0015/image. It is the closest comparable
because it does captioning, OCR and object detection in one call, which is
Florence-2's surface. AWS Rekognition and Azure AI Vision sit at ~$0.001/image
for the same jobs. A frontier VLM's per-token vision pricing was considered and
rejected as the anchor: it would price a 0.77B specialist as though it were
GPT-4o.

**Derivation**, as a multiple of the June whisper row (`$0.0042/min` →
`$0.00007/audio-second` → `40592888126 wei`):

```text
$0.0015/image * 0.70 = $0.00105/image
$0.00105 / $0.00007 = 15
15 * 40,592,888,126 = 608,893,321,890 wei/image
```

| Capability | Offering / model | Work unit | Market anchor | 30% target | `amount_wei` | `per_units` |
|---|---|---:|---:|---:|---:|---:|
| `vision:image-analysis` | `florence-2-large` / `microsoft/Florence-2-large` | image | Google Cloud Vision ~$0.0015/image | $0.00105/image | `608893321890` | `1` |

Had the cheaper $0.001 anchor been chosen, the figure is 10× the whisper row:
`405928881260`. Recorded so the choice is visible.

**Sanity check against the catalog:** this lands image analysis at about
1/20th of a FLUX image (`12177000000000`), which is the right shape —
generating an image is far more work than describing one — where the
placeholder had it at roughly 1/487,000th.

**Metering stays per image.** Per-token would be fairer to the operator (a
one-word answer and a 500-word description cost the same today), but Florence-2
is a captioning/OCR model with short, bounded outputs; per-image is far easier
for a buyer to reason about; and the runner already claims it in a response
header.

## FLUX.1-dev — which anchor is in force

The June table's FLUX row records **$0.0175/image** (30% under a $0.025
anchor) as the recommended price, and separately labels **$0.021/image** (30%
under the older $0.03 anchor) as "comparison only". The shipped template
carries `12177000000000 wei/image`, which is the $0.021 figure.

On 2026-09-01 the operator confirmed the **template is correct as shipped**.
So the anchor in force is **$0.03/image**, and the price in force is
**$0.021/image = `12177000000000 wei`**. This document supersedes the June
row's recommendation; the June row itself is untouched.

| Capability | Offering / model | Work unit | Market anchor | 30% target | `amount_wei` | `per_units` |
|---|---|---:|---:|---:|---:|---:|
| `openai:images-generations` | `black-forest-labs/FLUX.1-dev` | image | FLUX.1 dev $0.03/image | $0.021/image | `12177000000000` | `1` |

(June's `12177866437800` and the template's `12177000000000` differ by
rounding the target to $0.021 before conversion; the template's figure stands.)

## Provenance

Decided with the operator in the plan 0045 walkthrough, §6
(`docs/exec-plans/active/0045-runner-self-description-and-catalog-corrections.md`).
The capability rename from `openai:vision` is recorded there too: OpenAI has no
vision endpoint, and `capability_id` is opaque to the protocol.
