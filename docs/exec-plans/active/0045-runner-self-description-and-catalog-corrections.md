---
plan: 0045
title: Runner self-description and catalog corrections
status: active
phase: implementing
opened: 2026-09-01
owner: harness
related:
  - "active plan 0043 — connected runners and the offer-only manifest pipeline (§237 already specifies 'the container's contract or an adapter profile'; this plan builds the half that was never built and deletes the other)"
  - "active plan 0044 — zero-touch pool onboarding (placement, templates, ladder; §4 here changes what a template may say)"
  - "docs/references/2026-06-15-openai-compatible-market-pricing.md (superseded for the FLUX row by a new dated reference under §5)"
audience: broker / agent / pool-controller maintainers, runner image authors, operators
---

# Plan 0045 — Runner self-description and catalog corrections

**Status:** implementing — six decisions locked with the operator on
2026-09-01; every in-repo piece landed the same day. What remains lives in
other repositories and is listed under §10.

## 1. Purpose

> **A runner says what it is. Nothing else guesses.**

Six decisions, taken one at a time. Three of them (§2, §3, §6) are the same
decision seen from different sides: the runner declares itself and serves the
protocol's shape, and every mechanism that existed to guess on its behalf is
deleted. Two (§4, §5) are catalog corrections that stop a shipped template
lying about what it needs or what it is. One (§1) closes a control loop that
has never been closed.

The governing constraint, from the operator: **a runner that does not adhere
gets rewritten.** No compatibility shims, no second mechanism kept "just for
the old ones". Where an existing runner is affected we document it and
coordinate with its author — and for every runner in scope today, that author
is us.

## 2. Backend outcomes: close the scoring loop

`poolreport.ReportBestEffort` has no callers. `Server.poolReporter` is
constructed from `pool_snapshot` config and never read. The controller's end
is fully built — the route, `ApplyBackendOutcome`, the scoring settings — and
the broker pulls the resulting snapshot back to weight selection. The loop is
open at exactly one leg, so **every backend selection state sits at whatever
seeded it and scoring has never seen a real dispatch.**

**Decided.** No new policy is needed: the controller already encodes it.
`computeWindowSuccessScore` counts only `success` and `backend_failure`, so a
bad caller cannot degrade a member's score. `computeWindowLatencyScore`
includes `success` and `caller_failure` but drops `backend_failure`, because a
failure's duration says nothing about speed. The broker classifies honestly
into those three buckets and the receiving end does the rest.

- Only exchanges **dispatched to a runner** produce an outcome. The broker's
  own refusals — payment required, `capability_not_served`, protocol mismatch
  — never reach a runner and must not be attributed to a member's card.
- **Latency is time to first byte**, one definition for every transport,
  measured from dispatch to the runner's response headers. Total duration is
  wrong for streams: a 6-second stream is 6 seconds because the caller asked
  for 6 seconds of content, and reporting it would push work away from every
  long-stream offering. TTFB is comparable across unary and stream, which
  matters because the controller pools all samples for a backend into one p95
  against a single `LatencyTargetMS`. It is already the repo's vocabulary —
  certification's latency step takes `measure: first_byte`.
- **Paid-job only.** Sessions have no single completion moment, so both the
  outcome and the latency are ill-defined for them. Their own bead.

**Shape.** Carry the selected backend out of `handleJob` on the request
context (the existing `WithPendingDebitSlot` pattern) and report once from the
idempotency layer, where the exchange is already classified `ok` /
`client_error` / `backend_error` for every transport.

## 3. The runner self-description contract

Plan 0043 §237 says the agent builds the attach document "from the container's
contract **or** an adapter profile". Only profiles were ever built. The
consequence is that **adding a capability requires shipping a new agent to
every member** — a fleet operation, on machines we do not own, for a product
decision.

**Decided: one mechanism, mandatory, no fallback.**

- The contract is **specified in `livepeer-network-protocol/protocols/`** and
  versioned, because it is a boundary between the image author and the agent,
  not an agent implementation detail. The runner serves its own capability
  entry — protocol, paths or descriptor schemas, transports, work unit and
  extractor, readiness, identity — at a well-known endpoint, read **once at
  attach**. Not polled: polling is what 0043 item 11 deleted.
- **`pool-member-agent` becomes a relay.** Fetch the contract, add host id,
  credential and hardware, send. No capability knowledge in the agent, ever.
- **`internal/attach/profiles.go` is deleted**, and `profileFor` with it. That
  function currently guesses a runner's wire shape from a substring of its
  capability id (`strings.Contains(capability, "transcode")`), which is a
  symptom of the missing contract rather than a design.
- **No session profiles are built.** The contract states the protocol, so
  session-versus-job stops being an agent concern. This was going to be a
  first stage and is now dead work.
- **A missing contract fails loudly and names the container and the
  endpoint.** That message is the impacted-runner inventory — generated, not
  hand-maintained.

**Why now.** No template carries a `runner_compose` image yet. There is not
one deployed runner this breaks. The cost is a section in a spec today, or a
rewrite per image later.

**Where the boundary sits.** The runner is whatever terminates the
capability's HTTP surface, and that thing serves the contract. Because the
pool is the serving authority — ollama, vLLM, SGLang and third-party
passthrough are backends of *our* adapter, not a member's choice — every
runner is pool-shipped and the mandate is entirely within our control.

**Passthrough offerings** (LLM or any third-party integration) are
orchestrator-owned runners with no GPU, on a broker the pool does **not**
manage, merged into the manifest by the coordinator, which already scrapes
many brokers. This needs no code: attach declares `hardware` "required (may be
empty)", and `hardwareSatisfies` only gates capabilities that declare
`requirements`. It is deliberately **not** a pool-controller feature — a
hardware-less template would put a special case through placement, desired
state, the ladder and payouts.

**`member_env` is deleted** — the `RunnerCompose.MemberEnv` field, the
desired-state `RequiredEnv`, and the compose `${NAME}` passthrough. No
template uses it, and the case it existed for (the member brings their own
vLLM) is withdrawn.

## 4. Runner shape, images, and vendor variants

**Two families, deliberately different.**

- **OpenAI-compatible** (chat, three transcriptions, speech, images,
  image-analysis): **adapter + engine**. One adapter image for all seven, with
  the family — path, transports, work unit, extractor, readiness — as
  configuration. That table is `openaiFamilies` relocated out of the agent
  into the runner, where changing it does not require a fleet-wide rollout.
  Engine choice (ollama / vLLM / SGLang / passthrough) becomes an env var, not
  a template family.
- **Transcode** (vod, abr, live): the runner is **one container, ours**,
  serving its own contract and the protocol's shape directly, rather than
  being wrapped in something that translates for it.

**Image mapping**, confirmed against
`livepeer-modules-transcode-runners/README.md` and `API.md`:

| Template | Image | Endpoint |
|---|---|---|
| `video:transcode.vod` | `transcode-runner-{nvidia,intel}` | `POST /v1/video/transcode` |
| `video:transcode.abr` | `abr-runner-{nvidia,intel}` | `POST /v1/video/transcode/abr` |
| `video:transcode.live` | `live-runner-{nvidia,intel}` | `POST /v1/video/live/sessions` |

`transcode-runner-*` is "Single-rendition VOD transcode" in the repo's own
table — **vod and transcode are the same thing.**

**Vendor variants.** `runner_compose.image` becomes a **per-vendor map**, not
a string, resolved by the controller at desired-state render where the
hardware unit is known. An explicit map rather than `{{gpu.vendor}}`
substitution, on purpose: a template with no image for a vendor must make that
card **ineligible at placement**, which a map makes checkable when the catalog
loads. A templated string always produces a name, and it is wrong on a
member's host rather than in review.

Scope is **NVIDIA and Intel**; AMD waits for a later iteration and simply has
no key in the map.

**New validation:** the catalog's `Validate()` rejects a template whose
`gpu_classes` admit a vendor the image map cannot serve. A field failure
becomes a review failure.

**Intel is inert without class work.** All eight classes in `class.go` are
NVIDIA, and all three transcode templates require `gpu_classes: [rtx-2080]`,
so an Intel card gets `ClassUnknown` and is rejected. Intel needs classes in
`class.go` **and** those classes on the templates **and** the image map — any
one alone does nothing.

## 5. Hardware requirements for the three unconstrained templates

`flux-1-dev`, `nemo-meeting` and `nemo-meeting-stream` ship `requirements: {}`.

**The live problem is the opposite of the one recorded.** Nothing in the
catalog admits `a100`, `h100`, `l40s` or `rtx-3090` — chat needs a 5090,
transcode needs a 2080, the rest need 4090-or-5090. So the highest-priority
template admitting *everything* wins those cards: `nemo-meeting`, batch ASR of
a 0.6B model, at priority 12. **An H100 in the fleet is placed on batch
meeting transcription.** An empty list is not merely permissive; it is the
default winner on every card the rest of the catalog declines.

**The asymmetry that decides the two NeMo templates.** Their failure modes
differ in kind. `nemo-meeting` is batch: a weak card makes a transcription
slow, which is recoverable — the caller waits, the latency step is
non-required, the ladder scores it down. `nemo-meeting-stream` is a paid
session metered per wall-clock second: a card that cannot hold realtime does
not degrade, it **fails**, falling behind without bound while billing by the
second. Scoring cannot fix that. So batch is deliberately wide and streaming
deliberately narrow.

| Template | `gpu_classes` | Priority |
|---|---|---|
| `nemo-meeting` | `[rtx-2080, rtx-3090, rtx-4090, rtx-5090]` | 12 (unchanged) |
| `nemo-meeting-stream` | `[rtx-3090, rtx-4090, rtx-5090]` | 11 → **24** |
| `flux-1-dev` | `[rtx-4090, rtx-5090]` | 10 → **28** |

FLUX.1-dev is ~12B and ~24GB in bf16; an 8GB 1080 cannot run it at all.
Streaming ASR needs real margin, and a live session should win a card both fit
— below `transcode-live` (26), which is the same argument one product up.

Two caveats on the record: the exact class lists are fleet knowledge, not
derivable here, and **flux at 28 is a commercial call** — it puts generation
ahead of vision on a 4090. If that is wrong, move the priority, not the class
list.

**Deliberate consequence.** `a100`, `h100`, `l40s` — and `gtx-1080`, since the
lightest workload now starts at `rtx-2080` — become unplaceable: nothing in
the catalog sells their time. That is correct and visible —
placement records a per-unit rejection with a reason, surfaced on the
exception queue. An idle H100 someone can see beats an H100 quietly doing 0.6B
ASR. But any datacenter card in the fleet stops earning when this lands, and
that wants a template of its own rather than a loosened list.

Also update the vendor note in each file's `extra..hardware`, currently the
only record of the NVIDIA intent.

## 6. `openai:vision` is not a thing

OpenAI has no vision endpoint, so `openai:vision` claims one that does not
exist, and Florence-2 is not a chat model.

`capability_id` is **"Opaque; no closed enum"** (runner-attach §3.2 and
`schema.json`), so no convention is imposed by the protocol, and the catalog
itself already names products rather than endpoints — `video:transcode.vod`,
`livepeer:meet/sfu-room`.

**Decided: rename to `vision:image-analysis`**, following the catalog's own
`<domain>:<what it does>` shape. Considered and rejected: `openai:chat-
completions` (true to the wire, but names a vision model a chat model) and
`vision:image-captioning` (undersells it — Florence-2 also does OCR, detection
and region description).

**No runner change.** The runner declares its own `paths.invoke` and the
frozen offer publishes it in the manifest, so it keeps serving the
OpenAI-shaped request and a buyer still knows to use an OpenAI client. Only
the template's `capability` line changes; `match` stays as it is.

**Price:** `25000000` → **`608893321890`**, `per_units: 1` unchanged. Anchor
$0.0015/image (Google Cloud Vision — the closest comparable, since it does
captioning, OCR and object detection in one call, where a frontier VLM's
per-token vision pricing would price a 0.77B specialist like GPT-4o); 30%
under is $0.00105/image. Computed as exactly 15× the reference's whisper
figure so it inherits the 2026-06-15 basis with no rounding drift and is
checkable against a row already in that document. That is a ~24,000× increase
on an admitted placeholder, landing image-analysis at ~1/20th of a FLUX image
— the right shape, since generating an image is far more work than describing
one.

**Metering stays per image.** Per-token would be fairer (a one-word answer and
a 500-word description currently cost the same), but Florence-2 is a
captioning/OCR model with short bounded outputs, per-image is far easier for a
buyer to reason about, and the runner already claims it in a response header.

**FLUX is correct as shipped** at `12177000000000` ($0.021/image). That makes
the *reference* the stale artifact: its FLUX row records $0.0175 as the
decision while $0.021 is labelled "comparison only". `docs/references/` is
point-in-time and is not edited, so this is recorded in a **new dated
reference** carrying the vision derivation and the live FLUX anchor.

## 7. Transcode is asynchronous and therefore unbillable

`POST /v1/video/transcode` returns **`202` with a `job_id`**; status is polled
by a second POST; "upload and download URLs are caller-provided".

A 202 proves the job was *accepted*, not that it ran. There is nothing for the
extractor to count, so **an async transcode runner cannot be billed at all** —
certification is only where it surfaces first. `vod` and `abr` are
unshippable, not merely uncertifiable.

**The broker already has the right shape.** `internal/extractors/ffmpegprogress`
parses FFmpeg `-progress pipe:1` lines from the response body, with a
`LiveCounter` sibling so the interim-debit ticker sees running usage
mid-flight. That is a complete implementation of streamed, live-metered
transcode over the paid-job `stream` transport the broker already serves.

**Decided: change the runners.** They are ours. `POST /v1/video/transcode` and
`/abr` return a **streamed ffmpeg-progress body** and terminate with the
claim — synchronous, live-metered, no polling. The broker side needs no work.
`live-runner` needs nothing: its create/status/terminate already match
paid-session and it already returns `runner_session_id`, the field
`sessionengine` reads.

**Rejected:** declaring multipart (contradicts the product — the design is
caller-provided URLs, not pushed bytes); a JSON schema alone (a well-formed
request returning an acceptance receipt with no units); a fixture as a
`data:` URL (the runner *fetches* URLs).

**One new mechanism, small.** Certification needs a source video the runner
can fetch and somewhere to write output. Both become **run-scoped URLs the
broker mints**, exactly like the session usage callback: a fixture URL serving
`video/mp4-2s-720p`, and a sink that accepts and discards. A JSON request step
can then be authored honestly with `{{fixture_url.video/mp4-2s-720p}}`. Three
run-scoped URLs, one mechanism.

## 8. What this deletes

- `pool-member-agent/internal/attach/profiles.go` and `profileFor`
- `RunnerCompose.MemberEnv`, `desiredstate` `RequiredEnv`, the compose
  `${NAME}` passthrough
- `requirements: {}` as an expressible catalog state
- the `openai:vision` capability id
- polling as the transcode runners' completion mechanism

## 9. Open, deliberately

- Datacenter cards (`a100`, `h100`, `l40s`) have no template. Wanted: one of
  their own, not a loosened list. `gtx-1080` is declined by the same logic
  from the other end — if 8GB cards should earn, that is a template decision
  too.
- Session backend outcomes (§2 is paid-job only).
- AMD vendor images and classes.

## 10. Implementation record

Landed in this repository, 2026-09-01, in this order:

| § | Commit | What |
|---|---|---|
| 2 | `6529bbb` | Broker reports dispatch outcomes; TTFB; refusals excluded; paid-job only. |
| 5, 6 | `170fd3c` | Three templates get hardware policy; `vision:image-analysis` rename and price; new dated pricing reference. |
| 4 | `16111f6` | Per-vendor image map with load-time validation; `no_image_for_vendor`; Intel classes; vendor-branched device block; `member_env` deleted. |
| 3 | `949d669` | `runner-contract.md`; agent is a relay; profiles deleted; `draining` reaches the wire. |
| 7 | `68e5abf` | Run-scoped fixture and sink URLs; `{{fixture_url.*}}` / `{{sink_url}}`; transcode smoke steps in the real shape. |

**Remaining, outside this repository:**

- **`livepeer-modules-transcode`** (§7, `lnm-z72`) — the live repository;
  `livepeer-modules-transcode-runners` is a stale June extraction and the
  §7 evidence was re-verified against the live one on 2026-09-02. The runner
  images must (a) return a streamed `ffmpeg -progress` body from
  `POST /v1/video/transcode` and `/abr` and terminate with the claim — the
  runner already parses that progress internally, so this is plumbing — and
  (b) serve `GET /.well-known/livepeer-runner`. Until then the transcode
  templates fail certification loudly, naming the image.
- **`livepeer-modules-openai-runners`** (§3, §4): the OpenAI runner images
  **already exist**, one per capability, and `openai-chat-runner` is already
  the adapter — a Go proxy with `UPSTREAM_KIND`/`UPSTREAM_URL` in front of
  vLLM or Ollama; passthrough is one more `UPSTREAM_KIND`. §4's "one adapter
  image, family as configuration" is **withdrawn**: it was made without this
  repository in view, and re-architecting seven working images into one buys
  nothing once each serves its contract. What that repository needs: each
  image serves `/.well-known/livepeer-runner` in place of `GET
  /<capability>/options` (the deleted describe surface, whose fields map onto
  the contract nearly one to one); its capability ids move to the catalog's
  colon form (`openai-chat-completions` → `openai:chat-completions`,
  `image-generation` → `openai:images-generations`, `openai-text-embeddings`
  → `openai:embeddings`), which `RUNNERS.md` has already half-started; and its
  `BROKER-CONTRACT.md`, which still describes the broker dialling runners and
  merging `/options` into host-config, is rewritten to attach and contract.
- **`livepeer-modules-transcode-gateway`**: written against the deleted v0
  dispatch surface — it polls `brokerURL/v1/video/transcode/abr/status` and
  its live path reads `GET /v1/cap/{bsess}`. Independent of this plan, and
  broken today; the port target is paid-job `stream` and paid-session.
- **No runner exists anywhere for NeMo (two templates) or Florence-2.** Three
  catalog templates have no image in any repository.
- **Agent hardware inventory** (§4, found on the way): `collectNVIDIAGPUs`
  is the only inventory, so an Intel host reports no hardware and is never
  placed. Needed before an Intel card can earn.
