# Capability enum mapping (advisory)

Translation reference between this repo's opaque-string capability namespace and `go-livepeer`'s integer-bitmask `Capability_*` enum (defined in `core/capabilities.go`). Neither side is canonical; this table is a courtesy for consumers that need to bridge.

| go-livepeer enum             | Suggested string                          | Notes |
|------------------------------|-------------------------------------------|-------|
| `Capability_TextToImage = 27`     | `livepeer:ai/text-to-image`           | Stable Diffusion family |
| `Capability_ImageToImage = 28`    | `livepeer:ai/image-to-image`          | |
| `Capability_ImageToVideo = 29`    | `livepeer:ai/image-to-video`          | |
| `Capability_Upscale = 30`         | `livepeer:ai/upscale`                 | |
| `Capability_AudioToText = 31`     | `livepeer:ai/audio-to-text`           | Whisper family |
| `Capability_LLM = 33`             | `openai:/v1/chat/completions`         | OpenAI-compatible chat |
| `Capability_LiveVideoToVideo = 35`| `livepeer:ai/live-video-to-video`     | Live AI pipelines |
| `Capability_TextToSpeech = 36`    | `livepeer:ai/text-to-speech`          | |
| `Capability_TextToText`           | `openai:/v1/completions`              | Legacy completion API |
| `Capability_*` (transcoding profiles) | `livepeer:transcoder/{codec}`     | h264, hevc, av1, ... |

## Convention

- For **AI pipelines** that have a 1:1 OpenAI-API equivalent, prefer the `openai:` namespace (consumers often have OpenAI-compatible client libraries and benefit from the literal API path).
- For **AI pipelines** without a clean OpenAI mapping, use `livepeer:ai/<name>`.
- For **transcoding**, use `livepeer:transcoder/<codec>`.
- For **operator-defined capabilities**, use a namespace the operator owns (e.g. `myco:<name>`).

## Why we don't enforce this

The registry is workload-agnostic ([core-beliefs §3](../design-docs/core-beliefs.md), [workload-agnostic-strings.md](../design-docs/workload-agnostic-strings.md)). Hard-coding the table above into the daemon would couple us to go-livepeer's evolution. Consumers that need translation can use this document as a starting point and ship their own mapping.

## Updating

This file is reference material. Updates do not require an exec-plan. Add new rows when go-livepeer adds a `Capability_*` constant; mark removed rows with `<deprecated since vX>` in a parenthetical.
