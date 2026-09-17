# Regional runner definitions and readiness

The initial regional catalog enables EU/US transcode, US Whisper transcription,
US Kokoro speech, and US Qwen chat. The deployment generator partitions these
across the four brokers. Other catalog entries are outside that initial set.

The three AI definitions now contain startup instructions. Audio runners load
their own models into persistent `/models` volumes. Chat is one assignment
containing a protocol proxy and a local vLLM engine, with persistent model and
compile caches. Only the engine receives the GPU; only the proxy attaches.
See [the generic lifecycle](../../../pool-controller/docs/runner-companions.md).

## Published runtime provenance

Read-only registry metadata was checked on 2026-09-16. These are existing
published images; this work did not publish or copy upstream implementation.
The templates pin these digests:

| Role | Published version | Manifest digest |
|---|---|---|
| Transcription | `tztcloud/openai-audio-runner:v2.0.0` | `sha256:355715003f8b26dc45098fa6ac1242aa592ed370ede9db1f8200b40fb419d1de` |
| Speech | `tztcloud/openai-tts-runner:v2.0.0` | `sha256:b3f4417d966c1ccfcf930cb89b77d456517daedb4f4e2e88c5ac0e5bb5f30369` |
| Chat proxy | `tztcloud/openai-chat-runner:v2.0.0` | `sha256:769ae95e93cfd9df37d3c8173a52d9ea428ef6dd2b63c0d9300b7277ca190ed7` |
| Chat engine | `vllm/vllm-openai:v0.27.0` | `sha256:07ea4e292adf3a26b05ac97114b28849cf4551a26beb1fbe7decd3842d752ed7` |

Runtime variables and discovery behavior come from the published runners'
[versioned interface documentation](https://github.com/Cloud-SPE/livepeer-modules-openai-runners/blob/bb14895e010a/RUNNERS.md).
Engine flags follow the [vLLM Docker interface](https://docs.vllm.ai/en/v0.27.0/deployment/docker/)
and the accepted model's [model card](https://huggingface.co/sakamakismile/Qwen3.6-27B-Text-NVFP4-MTP).
No external vendor inference endpoint is used.

Qwen weights are pinned with `--revision
6f194695406a3bc88a00573187d5b2eecf984a99`. The 8192-token context, four engine
slots and 75% GPU-memory target are conservative startup settings for a 32 GB
5090 alongside audio assignments, not measured capacity guarantees. Existing
broker capacity, price, model identity and total-token metering remain the
catalog policy. Actual co-resident load must pass before activation.

The published audio interfaces accept model IDs and cache paths but do not
expose a revision setting. First download resolves the upstream model; capture
the cache revision in hardware acceptance evidence and preserve it across
restarts. Observed upstream revisions on 2026-09-16 were
`06f233fe06e710322aca913c1bc4249a0d71fce1` (Whisper) and
`f3ff3571791e39611d31c381e3a41a3af07b4987` (Kokoro). These observations are not
a claim that the audio runtime enforces those revisions.

## Validation boundary

Controller/agent tests exercise the serialized desired-state boundary, exact
Compose output, UUID isolation, companion stop behavior and stable cache names.
Published audio images were inspected locally. Offline CPU startup correctly
failed when model files were absent; that is not an inference success.

This development host has a GTX 1650 with 4 GB VRAM, not the catalog's 4090/5090
hardware. GPU model loading, voice aliases, streaming token extraction under
real load, co-resident memory pressure, certification and promotion require
hardware acceptance. Track this in `lnm-l17.6.2.1` and the authorized rollout
in `lnm-l17.6.3`; neither may be inferred from image availability or YAML parsing.

Before activating a regional source, retain the resolved image IDs, model-cache
revisions, driver/toolkit versions, actual runner self-description, certification
results, paid unary/streaming usage evidence, and normal-restart results. Run
these through the regional broker, agent and ownership path. A runner that
cannot load its model remains unready and must not be advertised as eligible.

## Transcode runtimes

The VOD, ABR and live templates pin published v2.0.0 vendor builds. Registry
manifests and OCI source revision `9c726fdcad2af2b0d866a741b3d62297319e4b84`
were checked on 2026-09-16. Configuration follows the published
[transcode runtime interfaces](https://github.com/Cloud-SPE/livepeer-modules-transcode/tree/9c726fdcad2af2b0d866a741b3d62297319e4b84).
No upstream implementation or deployment configuration was copied.

| Role | Image repository | Manifest digest |
|---|---|---|
| VOD nvidia | `tztcloud/transcode-runner` | `sha256:ce0897c10695630efb666c03e71047c04888dc9c4d43148dec8de8505fd1da04` |
| VOD intel | `tztcloud/transcode-runner` | `sha256:84c4f3c04eb379f54f46f915ab0ae01cef9d696995994e24600516fa38c7749f` |
| ABR nvidia | `tztcloud/abr-runner` | `sha256:fcd01a412a58a360b88746f0fa38b973ce7bbcc108c53c62aa1ac022cb41bce8` |
| ABR intel | `tztcloud/abr-runner` | `sha256:e488c90b883559568ddfe7146919bb842561cda95ebeb1da2db5b3041bb254e9` |
| LIVE nvidia | `tztcloud/live-runner-nvidia` | `sha256:27b4a735e22e6e34565d18dacad665e2f495635f4a872c285bd662cc3c4d6137` |
| LIVE intel | `tztcloud/live-runner-intel` | `sha256:e9d21eb6fba8ded92df7cd10016323c0b936d02dcb706a6ea8177f124c5e1d7f` |

VOD and ABR use synchronous paid-job streams with typed progress and measured
megapixel trailers. Their `STATE_DIR` journals are mounted persistently. Live
uses a vendor-specific paid-session runtime, persisted encrypted state, and
three assignment-local startup secrets. The broker bearer is added only to
local authenticated tunnel dispatch. Public HTTP/RTMPS coordinates are supplied
by the controller from the member's public origin; the image retains its baked
hardware and preset selection. All three retain shared GPU admission locks.
NVIDIA driver capabilities include video for encoder access.

Named journal volumes are required recovery state, even though the generic
mount field is named `caches`. Preserve them with the assignment's secret files;
see [manual restore](backup-and-restore.md). Model caches and operation journals
have different recovery requirements.

Local image metadata and parser checks do not prove hardware encoding. NVIDIA
and Intel startup without their devices fails closed; a CPU live image is a
separate product and is never substituted into these GPU templates. Actual
encode, RTMPS publishing, billed output, restart reconciliation and supported
Intel/NVIDIA device coverage remain hardware acceptance requirements.

## Pool labels and execution location

EU and US identify independent regional management and settlement pools. As
specified in the accepted design, affiliation does not prove GPU geography.
The shared transcode catalog therefore does not advertise a fixed execution
`constraints.region`; it also omits a single `gpu_vendor` because both Intel
and NVIDIA builds are eligible. A future geographical guarantee needs an
explicit admission and verification policy. Renaming a pool or deploying its
broker in a region does not supply that evidence.

The reproducible local probe `python3 check-live-runner-startup.py` uses the
already-present published CPU live image (never a production GPU fallback).
On 2026-09-16 it passed required configuration, discovery, 204 readiness,
private bearer enforcement, persisted session/descriptor replay after a normal
container restart, and termination with `gateway_close`. The script prints its
result JSON to stdout and writes no file (that run's output was captured in
`/tmp/regional-live-startup-probe.json`); the script removes its temporary
container, secrets and journal volume. It publishes no media and does not test
callback delivery or GPU encoding. The probe exposed the missing required live
session parameters in the catalog certification recipe; that recipe now uses
`live-standard`, metering rendition `720p`, and credential-free `runner-local`
storage. Hardware validation is tracked separately in `lnm-l17.6.2.6`.
