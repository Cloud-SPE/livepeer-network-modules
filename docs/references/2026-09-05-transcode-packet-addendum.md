# Addendum to the transcode runner packet (2026-09-02)

Date: 2026-09-05. Read with
`2026-09-02-runner-migration-packet-transcode.md`; three things landed in
`livepeer-network-modules` after it was written and change what the
packet asks for.

## 1. A CPU SVT-AV1 image now has a home (plan 0047)

The packet's question 3 asked whether `codecs-builder` produces an
SVT-AV1 build. The pool can now place on a CPU socket: the catalog carries
`video-transcode-vod-av1-cpu` (`video:transcode.vod`, offering
`vod-av1-cpu`, sockets of 16+ cores) with its image unresolved. The tag
belongs under the `cpu` key:

```yaml
runner_compose:
  image:
    nvidia: tztcloud/transcode-runner-nvidia:<tag>
    intel:  tztcloud/transcode-runner-intel:<tag>
    cpu:    tztcloud/transcode-runner-cpu:<tag>
```

The CPU build receives the same request shape as the GPU ones plus
`codec: av1`, streams the same `ffmpeg -progress` body, and serves the
same contract with `identity.provider` naming the encoder. No device is
passed to the container; it sees the host's cores as any container does.

## 2. Pascal: a class key, if you need one

If the nvidia build's CUDA base has dropped `sm_61`, the catalog can carry
a separate build for the 1080 class rather than dropping the card:
`nvidia/gtx-1080: <cu126 tag>` beside the `nvidia` default (landed
2026-09-04 for the openai-runners' Pascal variants). NVENC needs only the
driver, so a transcode image may not need this at all — tell us which.

## 3. The live runner learns its public address from the environment (plan 0046)

`video-transcode-live` is a `paid-session` runner and every session data
plane is external: the caller connects to the runner's `rtmp_url` and
`hls_url` directly. On a pool host the agent serves the member's public
edge and the pool sets `LIVEPEER_PUBLIC_URL=<https origin>/r/<service>`
on the container. Build both descriptor urls from it (`rtmps://` and
`https://` under that path); never from a guessed hostname. A host without
a public address is never placed on the live template, so the variable is
always present where the runner runs.
