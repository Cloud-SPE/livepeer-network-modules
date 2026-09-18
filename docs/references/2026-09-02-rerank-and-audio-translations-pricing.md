---
title: Rerank and audio-translations pricing derivation
date: 2026-09-02
status: reference
audience: operators, pricing work
---

# Rerank and audio-translations pricing derivation

Provenance for the two `price_default` values introduced with the
`text-rerank-zerank-2` and `openai-audio-translations-whisper-large-v3`
templates on 2026-09-02. The June snapshot
(`2026-06-15-openai-compatible-market-pricing.md`) has no row for either
capability, and it is point-in-time, so the derivation lives here.

Both figures **inherit the June ETH basis** (`ETH/USD = 1,724.44`) on
purpose: every other price in the catalog is expressed against it, and a
price computed on a different day's basis would be incomparable with its
neighbours rather than more accurate. Re-derive the whole catalog together
when the basis moves, not one row.

## `openai:audio-translations` — whisper-large-v3

Same runner, same weights, same compute as `openai:audio-transcriptions`:
Whisper translation is transcription with the decoder forced to English.
OpenAI prices its `/v1/audio/translations` and `/v1/audio/transcriptions`
endpoints identically ($0.006/min), so the anchor is the same anchor.

**Decision:** the template carries the transcription sibling's operator
rate, `45000000000` wei per audio second, unchanged. Reprice the two
together.

## `text:rerank` — zerank-2

Work unit: `documents` — one unit per document scored against the query.
Chosen over tokens (Voyage, Jina) because a caller sizes a rerank call by
document count, not by token count, and over "searches" (Cohere) because a
search's cost scales with how many documents it carries.

| Anchor | Public price | Per document |
|---|---:|---:|
| Cohere Rerank 3.5 | $2.00 per 1,000 searches, ≤100 documents each | $0.00002 at full search |
| Voyage `rerank-2` | $0.05 per 1M tokens | ≈$0.00001 at ~200 tokens/document |
| Jina `jina-reranker-v2` | $0.02 per 1M tokens | ≈$0.000004 at ~200 tokens/document |

Cohere is the selected anchor: it is the widely recognised managed rerank
baseline, and its per-document figure is the ceiling of the three, which is
where a 30%-under target should start.

```text
anchor      $0.02   per 1,000 documents  (Cohere at 100 docs/search)
30% target  $0.014  per 1,000 documents
amount_wei  0.014 / 1,724.44 × 1e18 = 8,118,577,625   per_units 1000
```

That is numerically the June snapshot's `nomic-embed-text-v2-moe` row
($0.014 per 1,000 tokens); the coincidence is the shared 30%-under-$0.02
arithmetic, not a relationship between the products.

## Sources

- Cohere pricing page, Rerank 3.5 row, read 2026-09-02.
- Voyage AI pricing page, `rerank-2` row, read 2026-09-02.
- Jina AI pricing page, reranker row, read 2026-09-02.
- OpenAI pricing page, audio transcription and translation rows, read
  2026-09-02.
