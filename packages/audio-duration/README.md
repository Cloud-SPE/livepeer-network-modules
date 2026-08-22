# `@livepeer-network/audio-duration`

The canonical `multipart-audio-duration/v1` estimator: an exact
whole-second ceiling for an uploaded audio file, or a refusal.

```ts
import { estimateCeilingSecondsFromMultipart } from '@livepeer-network/audio-duration';

const seconds = estimateCeilingSecondsFromMultipart(body, req.headers['content-type']);
// fund `seconds` units — or catch, and decline the upload as unsupported
```

## Why this exists

A transcription offering bills by the playing time of the audio it was
given. A caller that reserves funds **before** the work runs has to reach
the same number the broker will bill afterwards. If the two disagree, the
settlement exceeds the ceiling and a correct exchange is refused.

So this is a port of the broker's implementation, pinned to it by the
shared fixtures in
`livepeer-network-protocol/extractors/fixtures/multipart-audio-duration-v1`.
Both sides run those. A disagreement is a test failure here rather than
an incident in production.

## It refuses rather than guesses

`estimateCeilingSeconds` throws when the duration cannot be measured
exactly. A ceiling that reads low underfunds real work and one that reads
high overcharges, so neither is guessed at.

In practice only **headerless constant-bitrate MP3** reaches this: with no
Xing/Info/VBRI frame count there is no declared length, and a bitrate
estimate on a variable-bitrate file can be far out. Treat the throw as
"this upload is not fundable" and decline it.

| Container | Source of truth | Estimable |
|---|---|---|
| WAV | `data` chunk over the `fmt ` byte rate | yes |
| FLAC | STREAMINFO total samples over sample rate | yes |
| MP4 / M4A | `moov`→`mvhd` duration over timescale | yes |
| Ogg (Opus, Vorbis) | last page granule over sample rate | yes |
| WebM / Matroska | `Info` Duration × TimecodeScale | yes |
| MP3 with Xing/Info/VBRI | declared frame count | yes |
| MP3 without one | — | **no, refused** |

Also refused: unrecognized containers, truncated headers, a FLAC or
fragmented MP4 declaring no length, a WebM not yet finalized, and any
measurement beyond 24 hours (a misparse rather than a transcription
input).

## This is not settlement evidence

The estimate bounds what may be spent. The broker's **signed settlement**
is authoritative for what was. Never treat a locally computed duration as
proof of what an exchange cost.

## Rounding

Whole seconds, rounded **up**. A rule that could return `0` for delivered
work funds nothing for it.
