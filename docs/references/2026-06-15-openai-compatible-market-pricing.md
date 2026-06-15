---
title: OpenAI-compatible workload market pricing snapshot
date: 2026-06-15
status: reference
audience: operators, pricing work, gateway authors
---

# OpenAI-compatible workload market pricing snapshot

Point-in-time pricing references for the June 2026 OpenAI-compatible
capability set. Treat this as provenance for the prices operators were
considering on 2026-06-15, not as a live market feed.

Conversions use:

```text
ETH/USD = 1,724.44
1 USD = 1e18 / 1,724.44 wei
```

Source for the ETH/USD basis: Yahoo Finance `ETH-USD` historical row for
2026-06-15.

## Recommended Livepeer prices

These prices target roughly 30% below the selected public market anchor unless
noted otherwise.

| Capability | Offering / model | Work unit | Market anchor | 30% target | `amount_wei` | `per_units` |
|---|---|---:|---:|---:|---:|---:|
| `openai:audio-transcriptions` | `whisper-large-v3` | audio second | OpenAI transcribe estimated $0.006/min | $0.0042/min | `40592888126` | `1` |
| `openai:audio-speech` | `kokoro` / `hexgrad/Kokoro-82M` | character | OpenRouter Kokoro $0.62/M chars | $0.434/M chars | `251675906381` | `1000` |
| `openai:embeddings` | `nomic-embed-text:latest` | token | budget open-weight anchor $0.01/M tokens | $0.007/M tokens | `4059288813` | `1000` |
| `openai:embeddings` | `nomic-embed-text-v2-moe:latest` | token | OpenAI `text-embedding-3-small` $0.02/M tokens | $0.014/M tokens | `8118577625` | `1000` |
| `openai:embeddings` | `qwen3-embedding:latest` | token | OpenRouter Qwen3 Embedding 8B $0.01/M tokens | $0.007/M tokens | `4059288813` | `1000` |
| `openai:images-generations` | `black-forest-labs/FLUX.1-dev` | image | FLUX.1 dev $0.025/image | $0.0175/image | `10148222031500` | `1` |

## Comparison notes

### Audio transcription

Current OpenAI pricing lists:

- `gpt-4o-transcribe`: estimated `$0.006 / minute`
- `gpt-4o-mini-transcribe`: estimated `$0.003 / minute`
- `gpt-realtime-whisper`: `$0.017 / minute`

The recommended `whisper-large-v3` price anchors to the common OpenAI
transcription price of `$0.006/min`, then applies a 30% discount:

```text
$0.006/min * 0.70 = $0.0042/min
$0.0042/min / 60 = $0.00007/audio_second
$0.00007 * 1e18 / 1,724.44 = 40,592,888,126 wei/audio_second
```

Do not anchor non-realtime Whisper-style batch transcription to
`gpt-realtime-whisper` unless the product is also realtime/streaming. A 30%
discount from `gpt-realtime-whisper` would be `$0.0119/min`, or
`115013183024 wei/audio_second`, which is much higher than the batch
transcription recommendation.

### Audio speech

OpenRouter lists `hexgrad/kokoro-82m` at `$0.62/M characters`. OpenAI `tts-1`
is a useful comparison point at `$15/M characters`, but it is not the right
anchor for a Kokoro offering because Kokoro's market is materially cheaper.

Recommended Kokoro price:

```text
$0.62/M chars * 0.70 = $0.434/M chars
$0.434 / 1,000,000 = $0.000000434/char
$0.000000434 * 1e18 / 1,724.44 = 251,675,906 wei/char
251,675,906 * 1000 = 251,675,906,381 wei/1000 chars
```

Comparison only: 30% below OpenAI `tts-1` would be `$10.50/M characters`, or
`6088933218900 wei/1000 chars`.

### Embeddings

OpenAI lists:

- `text-embedding-3-small`: `$0.02/M tokens`
- `text-embedding-3-large`: `$0.13/M tokens`

OpenRouter lists:

- `qwen/qwen3-embedding-8b`: `$0.01/M tokens`
- `qwen/qwen3-embedding-4b`: `$0.02/M tokens`

Ollama's `qwen3-embedding:latest` tag resolves to the 8B/latest line in the
Ollama model library, so the recommended Livepeer price anchors to OpenRouter's
Qwen3 Embedding 8B price:

```text
$0.01/M tokens * 0.70 = $0.007/M tokens
$0.007 / 1,000,000 * 1e18 / 1,724.44 = 4,059,289 wei/token
4,059,289 * 1000 = 4,059,288,813 wei/1000 tokens
```

For `nomic-embed-text-v2-moe`, no clean public per-token hosted price was found
for this exact model. The selected anchor is OpenAI `text-embedding-3-small`
because it is a widely recognized low-cost embedding baseline:

```text
$0.02/M tokens * 0.70 = $0.014/M tokens
$0.014 / 1,000,000 * 1e18 / 1,724.44 = 8,118,578 wei/token
8,118,578 * 1000 = 8,118,577,625 wei/1000 tokens
```

`nomic-embed-text:latest` is priced as a budget open-weight offering at the
same `$0.007/M tokens` target as Qwen3 Embedding 8B unless measured local
hardware costs require a higher price.

### Image generation

Useful FLUX.1 dev anchors:

- Black Forest Labs' FLUX.1 API announcement listed `FLUX.1 [dev]` at
  `2.5 cts/img` (`$0.025/image`).
- Puter lists `FLUX.1 [dev]` at `$0.025/image`.
- Older repo notes used Replicate Flux Dev at roughly `$0.03/image`.

Recommended price:

```text
$0.025/image * 0.70 = $0.0175/image
$0.0175 * 1e18 / 1,724.44 = 10,148,222,031,500 wei/image
```

Comparison only: 30% below the older `$0.03/image` anchor is `$0.021/image`,
or `12177866437800 wei/image`.

The payout-simulator cadence floor for `FLUX.1-dev` is
`18750000000000 wei/image`. At the ETH/USD basis above, that is about
`$0.0323/image`; it preserves payout cadence at the modeled volume but is not
30% cheaper than FLUX.1 dev market APIs.

## Source URLs

- OpenAI pricing: <https://developers.openai.com/api/docs/pricing>
- OpenAI `tts-1` model page: <https://developers.openai.com/api/docs/models/tts-1>
- OpenAI `text-embedding-3-small` model page: <https://developers.openai.com/api/docs/models/text-embedding-3-small>
- OpenAI `text-embedding-3-large` model page: <https://developers.openai.com/api/docs/models/text-embedding-3-large>
- OpenRouter Kokoro: <https://openrouter.ai/hexgrad/kokoro-82m>
- OpenRouter Qwen3 Embedding 8B: <https://openrouter.ai/qwen/qwen3-embedding-8b>
- OpenRouter Qwen3 Embedding 4B: <https://openrouter.ai/qwen/qwen3-embedding-4b>
- Ollama Qwen3 Embedding library: <https://ollama.com/library/qwen3-embedding>
- Black Forest Labs FLUX.1 API announcement: <https://bfl.ai/announcing-flux-1-1-pro-and-the-bfl-api/>
- Puter FLUX.1 dev model page: <https://developer.puter.com/ai/black-forest-labs/flux.1-dev/>
- Yahoo Finance ETH-USD history: <https://finance.yahoo.com/quote/ETH-USD/history/>
