# Consumer migration packet — `livepeer-workflow-kit`

Date: 2026-09-02. For the author of `shane-demo/livepeer-workflow-kit`
(last commit 2026-08-28). Read on 2026-09-02; quoted paths are from your
source. Tracked as `lnm-iyb`.

**Dependency order (decision 4):** port any time against the conformance
runner; verified once the Florence and NeMo runners (their packets) ship.

## The short version

The kit is on the deleted v0 surface in two places, and it names
capabilities in a third vocabulary. `livepeer_roboflow/runtime.py` calls
`broker_url + "/v1/cap"` — removed with plan 0043; the broker serves
`POST /v1/job`. `runners.toml` reaches runners **directly** by URL with
`readiness_url` pointing at `GET /openai:audio-transcriptions/options` —
the describe surface the runner contract replaced, on a runner the kit
should not be dialling at all. And the kit's keys
(`audio.transcribe.stream`, `vision.screen_understanding`) are neither the
runners' nor the catalog's.

`roboflow_livepeer_blocks/loc_jobs.py` already sends `Livepeer-Capability`
and `Livepeer-Payment`; that path is closer to current.

## What you can delete

| What you have | Why it existed | What replaces it |
|---|---|---|
| `/v1/cap` calls in `runtime.py` | v0 dispatch | `POST /v1/job` with `Livepeer-Protocol: paid-job/v1` (Aug-19 gateway packet has the header table). |
| `runners.toml` runner URLs and `/options` readiness | The kit dialled runners itself | Go through the broker. A consumer never reaches a runner; it selects an offering by capability id and the broker dispatches. Readiness is the broker's problem (certification). |
| The kit's own capability keys as broker-facing ids | No shared vocabulary | Catalog ids on the wire; keep your dotted keys internal if they are useful to you. |

## What changes

| Kit key | Broker-facing capability id | Protocol | Notes |
|---|---|---|---|
| `vision.screen_understanding` (Florence manifest, hard-coded `florence-2`) | `vision:image-analysis` | `paid-job/v1` unary | `POST /v1/job`, body `{ "image": …, "task": "ocr" \| "caption" \| "object_detection" … }`; `Livepeer-Work-Units` header = images. The Florence manifest carries `capabilityId`/`offeringId` only — no runner URL, no model name. |
| `audio.transcribe.stream` | `audio:transcribe.live` | `paid-session/v1`, descriptor `pcm-transcript/v1` | `POST /v1/session` → descriptor with `public.url` and a `stream-attach` grant; connect to the socket with the grant; PCM16 LE 16 kHz mono in, `event_type` JSON out (`descriptors/pcm-transcript.md`). Depends on the member-edge feature (`lnm-7cj`) for pool members; works against an orchestrator-hosted runner sooner. |
| batch transcription (if used) | `openai:audio-transcriptions` | `paid-job/v1` multipart | `model: nemo-diarized-transcription-meeting-v0`. |

The rule for any future id: runner-attach §3.2, "Capability id vocabulary".

## What is *not* fixed by this work

- The runners themselves (Florence and NeMo packets). Until they ship the
  contract, no pool advertises these capabilities and the kit has nothing
  to select.

## Verifying

```sh
cd livepeer-network-protocol/conformance
go run ./cmd/livepeer-conformance --broker-url http://<broker>:8080 --attach-runner
```

for a broker with fake job and session runners to port against; then the
shipped runners.

## What we need from you

1. Confirmation the kit no longer holds runner URLs.
2. Your list of any other capability ids the kit sends, so the catalog can
   check each against the vocabulary rule.
