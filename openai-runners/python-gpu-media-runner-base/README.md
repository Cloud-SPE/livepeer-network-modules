# python-gpu-media-runner-base

Shared CUDA Python media base for the OpenAI audio-style runners.

This image layers the common media runtime dependency set on top of
[`../python-gpu-runner-base/`](../python-gpu-runner-base/): today that is
`ffmpeg`, which both `openai-audio-runner` and `openai-tts-runner` need.

## Why it exists

`python-gpu-runner-base` removes duplicated Python/CUDA scaffolding across
GPU runners, but it intentionally does not ship workload-specific media
packages. The audio and TTS runners both need the same large `ffmpeg`
runtime, so this sibling base prevents that apt payload from being rebuilt
and stored twice.

## Build

```bash
docker build \
  --build-arg BASE_IMAGE=tztcloud/python-gpu-runner-base:v1.1.0 \
  -t tztcloud/python-gpu-media-runner-base:v1.1.0 \
  .
```

## Consumers

- `openai-audio-runner/`
- `openai-tts-runner/`
